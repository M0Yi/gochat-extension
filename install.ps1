#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Code = "",
    [string]$Mode = "",
    [string]$FromTarball = "",
    [switch]$Relay,
    [switch]$Local,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

$VERSION = "2026.4.9-plugin.33"
$EXTENSION_NAME = "gochat"
$REPO_TARBALL_URL = "https://codeload.github.com/M0Yi/gochat-extension/tar.gz/refs/heads/main"
$REMOTE_INSTALL_PS_URL = "https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.ps1"
$DEFAULT_RELAY_HTTP_URL = "https://fund.moyi.vip"
$DEFAULT_RELAY_WS_URL = "wss://fund.moyi.vip/ws/plugin"
$DEFAULT_LOCAL_PORT = 9750
$RELAY_HTTP_URL = if ($env:GOCHAT_RELAY_HTTP_URL) { $env:GOCHAT_RELAY_HTTP_URL } else { $DEFAULT_RELAY_HTTP_URL }
$RELAY_WS_URL = if ($env:GOCHAT_RELAY_WS_URL) { $env:GOCHAT_RELAY_WS_URL } else { $DEFAULT_RELAY_WS_URL }

$Script:Platform = "windows"
$Script:Arch = "unknown"
$Script:OpenClawBin = ""
$Script:OpenClawVersion = ""
$Script:NodeVersion = ""
$Script:NpmBin = ""
$Script:ModeSwitchChanged = $false

function Write-Info($msg)  { Write-Host "`e[36;1m[gochat]`e[0m $msg" }
function Write-Ok($msg)    { Write-Host "`e[32;1m[gochat]`e[0m $msg" }
function Write-Warn($msg)  { Write-Host "`e[33;1m[gochat]`e[0m $msg" }
function Write-Fail($msg)  { Write-Host "`e[31;1m[gochat]`e[0m $msg" }

function Exit-WithError {
    param([string]$Message)
    Write-Fail $Message
    exit 1
}

function Get-VersionTriplet {
    param([string]$RawVersion)
    if (-not $RawVersion) {
        return ""
    }
    $match = [regex]::Match($RawVersion, '\d{4}\.\d{1,2}\.\d{1,2}')
    if ($match.Success) {
        return $match.Value
    }
    return ""
}

function Get-VersionTripletKey {
    param([string]$Triplet)
    if (-not $Triplet) {
        return $null
    }
    $parts = $Triplet.Split(".")
    if ($parts.Length -lt 3) {
        return $null
    }
    return ("{0:D4}{1:D2}{2:D2}" -f [int]$parts[0], [int]$parts[1], [int]$parts[2])
}

function Warn-IfKnownPairingBugHost {
    param([string]$RawVersion)
    $triplet = Get-VersionTriplet $RawVersion
    if (-not $triplet) {
        return
    }
    $key = Get-VersionTripletKey $triplet
    if (-not $key) {
        return
    }
    if ([int64]$key -lt 20260408) {
        Write-Warn "OpenClaw $triplet is older than 2026.4.8 and is known to surface local subagent pairing-required failures."
        Write-Warn "GoChat $VERSION now surfaces subagent permission status and approval commands in chat, but upgrading OpenClaw is still recommended."
    }
}

function Get-GoChatCurrentMode {
    param([string]$ConfigFile)
    if (-not (Test-Path $ConfigFile)) {
        return ""
    }
    try {
        $raw = Get-Content -Path $ConfigFile -Raw
        return (Get-JsonValue $raw "channels.gochat.mode")
    } catch {
        return ""
    }
}

function Test-GoChatModeSwitchAuthorization {
    param(
        [string]$ConfigFile,
        [string]$TargetMode
    )

    if (-not (Test-Path $ConfigFile)) {
        return $false
    }

    try {
        & node -e @"
const fs = require('fs');
const cfg = JSON.parse(fs.readFileSync(process.argv[1], 'utf8'));
const targetMode = String(process.argv[2] || '');
const auth = cfg.channels && cfg.channels.gochat && cfg.channels.gochat.modeSwitchAuthorization;
if (!auth || auth.targetMode !== targetMode) process.exit(1);
if (auth.expiresAt) {
  const expires = new Date(auth.expiresAt);
  if (Number.isNaN(expires.getTime()) || expires.getTime() <= Date.now()) process.exit(1);
}
"@ $ConfigFile $TargetMode 2>$null | Out-Null
        return $LASTEXITCODE -eq 0
    } catch {
        return $false
    }
}

function Require-GoChatModeSwitchAuthorization {
    param(
        [string]$ConfigFile,
        [string]$TargetMode
    )

    $Script:ModeSwitchChanged = $false
    $currentMode = Get-GoChatCurrentMode $ConfigFile
    if (-not $currentMode -or $currentMode -eq $TargetMode) {
        return
    }

    if (Test-GoChatModeSwitchAuthorization -ConfigFile $ConfigFile -TargetMode $TargetMode) {
        $Script:ModeSwitchChanged = $true
        Write-Info "Using one-time mode switch authorization: $currentMode -> $TargetMode"
        return
    }

    Exit-WithError "Switching GoChat mode from $currentMode to $TargetMode requires explicit authorization. Run: openclaw gochat authorize-mode-switch --mode $TargetMode"
}

function Consume-GoChatModeSwitchAuthorization {
    param([string]$ConfigFile)

    if (-not $Script:ModeSwitchChanged) {
        return
    }

    try {
        & node -e @"
const fs = require('fs');
const file = process.argv[1];
const cfg = JSON.parse(fs.readFileSync(file, 'utf8'));
if (cfg.channels && cfg.channels.gochat && cfg.channels.gochat.modeSwitchAuthorization) {
  delete cfg.channels.gochat.modeSwitchAuthorization;
  fs.writeFileSync(file, JSON.stringify(cfg, null, 2) + '\n');
}
"@ $ConfigFile 2>$null | Out-Null
    } catch {
    }
}

function Get-JsonValue {
    param([string]$JsonData, [string]$Key)
    try {
        return (& node -e "const a=process.argv.slice(1),k=a[0],d=JSON.parse(a[1]);let v=d;for(const s of k.split('.')){if(v==null||v[s]===undefined){v=null;break}v=v[s]}if(v!==null&&v!==undefined)process.stdout.write(String(v))" $Key $JsonData 2>$null)
    } catch {
        return ""
    }
}

function Detect-Platform {
    $cpu = $env:PROCESSOR_ARCHITECTURE
    if ($cpu -match "AMD64|X64") {
        $Script:Arch = "amd64"
    } elseif ($cpu -match "ARM64") {
        $Script:Arch = "arm64"
    }

    Write-Info "Platform: $($Script:Platform) ($($Script:Arch))"
}

function Get-NpmCommandPath {
    $candidates = @()

    foreach ($name in @("npm.cmd", "npm.exe", "npm")) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd -and $cmd.Source) {
            $candidates += $cmd.Source
        }
    }

    $candidates += @(
        "${env:ProgramFiles}\nodejs\npm.cmd",
        "${env:ProgramFiles}\nodejs\npm.exe",
        "${env:APPDATA}\npm\npm.cmd",
        "${env:APPDATA}\npm\npm.exe"
    )

    foreach ($candidate in ($candidates | Where-Object { $_ } | Select-Object -Unique)) {
        if (-not (Test-Path $candidate)) {
            continue
        }
        $ext = [System.IO.Path]::GetExtension($candidate)
        if ($ext -in @(".cmd", ".exe", ".bat")) {
            return $candidate
        }
    }

    return $null
}

function Detect-OpenClaw {
    $oc = Get-Command "openclaw" -ErrorAction SilentlyContinue
    if ($oc) {
        $Script:OpenClawBin = $oc.Source
        return $true
    }

    $paths = @(
        "$env:USERPROFILE\.local\bin\openclaw.exe",
        "$env:USERPROFILE\.local\bin\openclaw.cmd",
        "$env:APPDATA\npm\openclaw.cmd",
        "$env:APPDATA\npm\openclaw.ps1",
        "${env:ProgramFiles}\openclaw\bin\openclaw.exe",
        "${env:LocalAppData}\openclaw\bin\openclaw.exe"
    )

    foreach ($path in $paths) {
        if (Test-Path $path) {
            $Script:OpenClawBin = $path
            return $true
        }
    }

    return $false
}

function Ensure-Prerequisites {
    $node = Get-Command "node" -ErrorAction SilentlyContinue
    if (-not $node) {
        Exit-WithError "Node.js is required but was not found. Install Node.js first: https://nodejs.org/"
    }

    $Script:NpmBin = Get-NpmCommandPath
    if (-not $Script:NpmBin) {
        Exit-WithError "npm is required but was not found. Reinstall Node.js or ensure npm.cmd is on PATH."
    }

    $Script:NodeVersion = & node --version 2>$null
    Write-Info "Node.js: $($Script:NodeVersion)"

    if (Detect-OpenClaw) {
        Write-Info "Found OpenClaw at: $($Script:OpenClawBin)"
        $Script:OpenClawVersion = & $Script:OpenClawBin --version 2>$null | Select-Object -First 1
        if ($Script:OpenClawVersion) {
            Write-Info "OpenClaw version: $($Script:OpenClawVersion)"
            Warn-IfKnownPairingBugHost $Script:OpenClawVersion
        }
    } else {
        Write-Warn "OpenClaw CLI not found. The extension will install, but OpenClaw must be installed before use."
    }
}

function Get-OpenClawDir {
    if ($env:OPENCLAW_STATE_DIR) {
        return $env:OPENCLAW_STATE_DIR
    }
    return Join-Path $env:USERPROFILE ".openclaw"
}

function Get-ExtensionsDir {
    return Join-Path (Get-OpenClawDir) "extensions"
}

function Get-SkillsDir {
    return Join-Path (Get-OpenClawDir) "skills"
}

function Ensure-DirWritable {
    param([string]$TargetDir)

    try {
        if (-not (Test-Path $TargetDir)) {
            New-Item -ItemType Directory -Path $TargetDir -Force | Out-Null
        }
        $testFile = Join-Path $TargetDir ".write-test-$([guid]::NewGuid().ToString('n').Substring(0,8))"
        Set-Content -Path $testFile -Value "ok" -ErrorAction Stop
        Remove-Item $testFile -Force
    } catch {
        Exit-WithError "Directory not writable: $TargetDir"
    }
}

function Remove-DirIfExists {
    param([string]$PathToRemove)

    if (Test-Path $PathToRemove) {
        Remove-Item $PathToRemove -Recurse -Force -ErrorAction Stop
    }
}

function Install-NpmDependencies {
    param([string]$TargetDir)

    if (-not (Test-Path (Join-Path $TargetDir "package.json"))) {
        Exit-WithError "package.json not found in installed extension directory."
    }

    Write-Info "Installing npm dependencies..."
    & $Script:NpmBin install --omit=dev
    if ($LASTEXITCODE -ne 0) {
        Exit-WithError "npm install failed."
    }
}

function Install-FromSource {
    param([string]$SourceDir)

    $extensionsDir = Get-ExtensionsDir
    $target = Join-Path $extensionsDir $EXTENSION_NAME
    Ensure-DirWritable $extensionsDir

    if (Test-Path $target) {
        Write-Info "Removing previous installation..."
        Remove-DirIfExists $target
    }

    New-Item -ItemType Directory -Path $target -Force | Out-Null
    Write-Info "Copying to $target..."

    Get-ChildItem -LiteralPath $SourceDir -Force | ForEach-Object {
        if ($_.Name -in @(".git", "node_modules")) {
            return
        }
        Copy-Item -LiteralPath $_.FullName -Destination $target -Recurse -Force
    }

    Push-Location $target
    try {
        Install-NpmDependencies $target
    } finally {
        Pop-Location
    }

    Ensure-PluginTrusted
    Write-Ok "Installed to $target"
    Install-BundledSkills $target
}

function Install-FromTarball {
    param([string]$TarballPath)

    $extensionsDir = Get-ExtensionsDir
    $target = Join-Path $extensionsDir $EXTENSION_NAME
    Ensure-DirWritable $extensionsDir

    if (Test-Path $target) {
        Write-Info "Removing previous installation..."
        Remove-DirIfExists $target
    }

    $tar = Get-Command "tar" -ErrorAction SilentlyContinue
    if (-not $tar) {
        Exit-WithError "tar is required to extract the installer payload on Windows."
    }

    $extractDir = Join-Path $env:TEMP "gochat-extract-$(Get-Random)"
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null

    Write-Info "Extracting to $target..."
    try {
        & $tar.Source -xzf $TarballPath -C $extractDir
        if ($LASTEXITCODE -ne 0) {
            Exit-WithError "Failed to extract installer payload."
        }

        $rootDir = Get-ChildItem -LiteralPath $extractDir -Directory | Select-Object -First 1
        if (-not $rootDir) {
            Exit-WithError "Installer payload was empty after extraction."
        }

        Move-Item -LiteralPath $rootDir.FullName -Destination $target -Force

        Push-Location $target
        try {
            Install-NpmDependencies $target
        } finally {
            Pop-Location
        }
    } finally {
        Remove-DirIfExists $extractDir
    }

    Ensure-PluginTrusted
    Write-Ok "Installed to $target"
    Install-BundledSkills $target
}

function Install-BundledSkills {
    param([string]$ExtensionDir)

    $sourceSkillsDir = Join-Path $ExtensionDir "skills"
    if (-not (Test-Path $sourceSkillsDir)) {
        return
    }

    $targetSkillsDir = Get-SkillsDir
    Ensure-DirWritable $targetSkillsDir
    Write-Info "Installing bundled skills to $targetSkillsDir..."

    Get-ChildItem -LiteralPath $sourceSkillsDir -Directory -Force | ForEach-Object {
        $destination = Join-Path $targetSkillsDir $_.Name
        if (Test-Path $destination) {
            Remove-DirIfExists $destination
        }
        Copy-Item -LiteralPath $_.FullName -Destination $destination -Recurse -Force
    }

    Write-Ok "Bundled skills installed"
}

function Install-Remote {
    $downloadDir = Join-Path $env:TEMP "gochat-download-$(Get-Random)"
    $tarballPath = Join-Path $downloadDir "gochat-extension.tar.gz"
    New-Item -ItemType Directory -Path $downloadDir -Force | Out-Null

    Write-Info "Downloading installer payload from GitHub..."
    try {
        $requestParams = @{
            Uri = $REPO_TARBALL_URL
            OutFile = $tarballPath
            ErrorAction = "Stop"
        }
        $iwr = Get-Command "Invoke-WebRequest" -ErrorAction Stop
        if ($iwr.Parameters.ContainsKey("UseBasicParsing")) {
            $requestParams.UseBasicParsing = $true
        }
        Invoke-WebRequest @requestParams
    } catch {
        Exit-WithError "Failed to download installer payload from GitHub. Check network access."
    } finally {
        if (-not (Test-Path $tarballPath)) {
            Remove-DirIfExists $downloadDir
        }
    }

    try {
        Install-FromTarball $tarballPath
    } finally {
        Remove-DirIfExists $downloadDir
    }
}

function Ensure-ConfigFile {
    param([string]$ConfigFile)

    $configDir = Split-Path $ConfigFile -Parent
    if (-not (Test-Path $configDir)) {
        New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    }
    if (-not (Test-Path $ConfigFile)) {
        Set-Content -Path $ConfigFile -Value "{`n}`n"
    }
}

function Ensure-PluginTrusted {
    $configFile = Join-Path (Get-OpenClawDir) "openclaw.json"
    Ensure-ConfigFile $configFile

    & node -e @"
const fs = require('fs');
const file = process.argv[1];
const pluginId = process.argv[2];
let cfg = {};
try { cfg = JSON.parse(fs.readFileSync(file, 'utf8')); } catch {}
if (!cfg || typeof cfg !== 'object' || Array.isArray(cfg)) cfg = {};
if (!cfg.plugins || typeof cfg.plugins !== 'object' || Array.isArray(cfg.plugins)) cfg.plugins = {};
const allow = Array.isArray(cfg.plugins.allow) ? cfg.plugins.allow.slice() : [];
if (!allow.includes(pluginId)) allow.push(pluginId);
cfg.plugins.allow = allow;
fs.writeFileSync(file, JSON.stringify(cfg, null, 2) + '\n');
"@ $configFile $EXTENSION_NAME 2>$null | Out-Null
}

function Get-GoChatConfigSnapshot {
    param([string]$ConfigFile)

    if (-not (Test-Path $ConfigFile)) {
        return [pscustomobject]@{
            Name = ""
            Mode = ""
            RelayUrl = ""
            ChannelId = ""
            Secret = ""
            DirectPort = ""
        }
    }

    $raw = Get-Content -Path $ConfigFile -Raw
    return [pscustomobject]@{
        Name = Get-JsonValue $raw "channels.gochat.name"
        Mode = Get-JsonValue $raw "channels.gochat.mode"
        RelayUrl = Get-JsonValue $raw "channels.gochat.relayPlatformUrl"
        ChannelId = Get-JsonValue $raw "channels.gochat.channelId"
        Secret = Get-JsonValue $raw "channels.gochat.webhookSecret"
        DirectPort = Get-JsonValue $raw "channels.gochat.directPort"
        BlockStreaming = Get-JsonValue $raw "channels.gochat.blockStreaming"
    }
}

function Get-DefaultDeviceName {
    $snapshot = Get-GoChatConfigSnapshot (Join-Path (Get-OpenClawDir) "openclaw.json")
    if ($snapshot.Name) {
        return $snapshot.Name
    }
    if ($env:COMPUTERNAME) {
        return "$($env:COMPUTERNAME)"
    }
    return "OpenClaw GoChat Plugin"
}

function New-RandomSecret {
    return ([guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")).Substring(0, 48)
}

function Write-GoChatConfig {
    param(
        [string]$ConfigFile,
        [string]$Mode,
        [string]$Name,
        [string]$RelayUrl = "",
        [string]$ChannelId = "",
        [string]$Secret = "",
        [string]$DirectPort = ""
    )

    Ensure-ConfigFile $ConfigFile

    & node -e @"
const fs = require('fs');
const configFile = process.argv[1];
const mode = process.argv[2];
const name = process.argv[3];
const relayUrl = process.argv[4];
const channelId = process.argv[5];
const secret = process.argv[6];
const directPort = process.argv[7];
let cfg = {};
try { cfg = JSON.parse(fs.readFileSync(configFile, 'utf8')); } catch {}
if (!cfg || typeof cfg !== 'object' || Array.isArray(cfg)) cfg = {};
if (!cfg.channels || typeof cfg.channels !== 'object' || Array.isArray(cfg.channels)) cfg.channels = {};
const current = cfg.channels.gochat && typeof cfg.channels.gochat === 'object' ? cfg.channels.gochat : {};
const next = Object.assign({}, current);
next.enabled = true;
next.name = name || current.name || 'OpenClaw GoChat Plugin';
next.dmPolicy = 'open';
next.blockStreaming = true;
if (mode === 'local') {
  next.mode = 'local';
  next.directPort = Number(directPort || 9750);
  next.webhookSecret = secret || current.webhookSecret || '';
  delete next.channelId;
  delete next.relayPlatformUrl;
} else {
  next.mode = 'relay';
  next.relayPlatformUrl = relayUrl || current.relayPlatformUrl || '';
  if (channelId) next.channelId = channelId;
  if (secret) next.webhookSecret = secret;
  if (!next.channelId) delete next.channelId;
  if (!next.webhookSecret) delete next.webhookSecret;
  delete next.directPort;
}
cfg.channels.gochat = next;
fs.writeFileSync(configFile, JSON.stringify(cfg, null, 2) + '\n');
"@ $ConfigFile $Mode $Name $RelayUrl $ChannelId $Secret $DirectPort 2>$null

    if ($LASTEXITCODE -ne 0) {
        Exit-WithError "Failed to write OpenClaw config."
    }
}

function Get-HttpErrorBody {
    param($ErrorRecord)

    try {
        if ($ErrorRecord.ErrorDetails -and $ErrorRecord.ErrorDetails.Message) {
            return [string]$ErrorRecord.ErrorDetails.Message
        }

        $response = $ErrorRecord.Exception.Response
        if (-not $response) {
            return ""
        }
        $stream = $response.GetResponseStream()
        if (-not $stream) {
            return ""
        }
        $reader = New-Object System.IO.StreamReader($stream)
        try {
            return $reader.ReadToEnd()
        } finally {
            $reader.Dispose()
            $stream.Dispose()
        }
    } catch {
        return ""
    }
}

function Resolve-ApiErrorMessage {
    param(
        [string]$Action,
        $ErrorRecord
    )

    $statusCode = $null
    try {
        if ($ErrorRecord.Exception.Response) {
            $statusCode = [int]$ErrorRecord.Exception.Response.StatusCode
        }
    } catch {
    }

    $body = Get-HttpErrorBody $ErrorRecord
    $serverError = ""
    if ($body) {
        $serverError = Get-JsonValue $body "error"
    }
    if ($serverError) {
        if ($serverError -eq "pair code expired") {
            return "Connection code expired. Generate a fresh 6-digit code and try again."
        }
        if ($serverError -eq "pair code already used") {
            return "Connection code was already used. Generate a fresh 6-digit code and try again."
        }
        if ($serverError -eq "pair code not found") {
            return "Connection code was not found. Double-check the 6-digit code or generate a new one."
        }
        return "$Action failed: $serverError"
    }

    if ($statusCode) {
        return "$Action failed (HTTP $statusCode)."
    }

    return "$Action failed: $($ErrorRecord.Exception.Message)"
}

function Invoke-RelayJson {
    param(
        [string]$Path,
        [hashtable]$Body,
        [int]$TimeoutSec = 15
    )

    return Invoke-RestMethod -Uri ($RELAY_HTTP_URL + $Path) `
        -Method POST `
        -ContentType "application/json" `
        -Body ($Body | ConvertTo-Json -Compress) `
        -TimeoutSec $TimeoutSec `
        -ErrorAction Stop
}

function Claim-ConnectionCode {
    param([string]$PairCode, [string]$DeviceName)

    Write-Info "Claiming connection code $PairCode..."
    try {
        $response = Invoke-RelayJson -Path "/api/plugin/pair/claim" -Body @{
            code = $PairCode
            name = $DeviceName
        } -TimeoutSec 20
    } catch {
        Exit-WithError (Resolve-ApiErrorMessage "Claim connection code" $_)
    }

    if (-not $response.channelId -or -not $response.secret) {
        $raw = $response | ConvertTo-Json -Depth 10 -Compress
        Exit-WithError ("Claim connection code returned an unexpected response: " + $raw)
    }

    return [pscustomobject]@{
        ChannelId = [string]$response.channelId
        Secret = [string]$response.secret
        Name = [string]$response.name
        RelayUrl = if ($response.relayPlatformUrl) { [string]$response.relayPlatformUrl } else { $RELAY_WS_URL }
    }
}

function Try-RegisterRelay {
    param([string]$DeviceName)

    Write-Info "Registering relay channel..."
    try {
        $response = Invoke-RelayJson -Path "/api/plugin/register" -Body @{
            name = $DeviceName
        }
    } catch {
        Write-Warn (Resolve-ApiErrorMessage "Relay registration" $_)
        return $null
    }

    if (-not $response.channelId -or -not $response.secret) {
        Write-Warn "Relay registration returned an unexpected response."
        return $null
    }

    return [pscustomobject]@{
        ChannelId = [string]$response.channelId
        Secret = [string]$response.secret
        Name = $DeviceName
        RelayUrl = $RELAY_WS_URL
    }
}

function Print-RelayStatus {
    param([string]$ConfigFile)

    $snapshot = Get-GoChatConfigSnapshot $ConfigFile

    Write-Host ""
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[36;1m  GoChat Relay Status`e[0m"
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "  Config File:   $ConfigFile"
    Write-Host "  Mode:          relay"
    Write-Host "  Relay URL:     $($snapshot.RelayUrl)"
    if ($snapshot.ChannelId) {
        Write-Host "  Channel ID:    `e[32m$($snapshot.ChannelId)`e[0m"
    } else {
        Write-Host "  Channel ID:    (not configured)"
    }
    if ($snapshot.Secret) {
        Write-Host "  Secret Key:    `e[32m$($snapshot.Secret)`e[0m"
    } else {
        Write-Host "  Secret Key:    (not configured)"
    }
    Write-Host "  DM Policy:     open"
    Write-Host "  Streaming:     enabled"
    Write-Host ""
}

function Print-LocalStatus {
    param([string]$ConfigFile)

    $snapshot = Get-GoChatConfigSnapshot $ConfigFile

    Write-Host ""
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[36;1m  GoChat Local Status`e[0m"
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "  Config File:   $ConfigFile"
    Write-Host "  Mode:          local"
    Write-Host "  Local Port:    $($snapshot.DirectPort)"
    if ($snapshot.Secret) {
        Write-Host "  Secret Key:    `e[32m$($snapshot.Secret)`e[0m"
    } else {
        Write-Host "  Secret Key:    (generated automatically)"
    }
    Write-Host "  DM Policy:     open"
    Write-Host "  Streaming:     enabled"
    Write-Host ""
}

function Configure-Relay {
    param([string]$PairCode)

    $configFile = Join-Path (Get-OpenClawDir) "openclaw.json"
    Require-GoChatModeSwitchAuthorization -ConfigFile $configFile -TargetMode "relay"
    $snapshot = Get-GoChatConfigSnapshot $configFile
    $deviceName = if ($snapshot.Name) { $snapshot.Name } else { Get-DefaultDeviceName }

    if ($PairCode) {
        $claimed = Claim-ConnectionCode -PairCode $PairCode -DeviceName $deviceName
        Write-GoChatConfig -ConfigFile $configFile -Mode "relay" -Name $claimed.Name -RelayUrl $claimed.RelayUrl -ChannelId $claimed.ChannelId -Secret $claimed.Secret
        Consume-GoChatModeSwitchAuthorization -ConfigFile $configFile
        Write-Ok "Connection code accepted. channelId=$($claimed.ChannelId)"
        Print-RelayStatus $configFile
        return
    }

    if ($snapshot.ChannelId -and $snapshot.Secret) {
        Write-GoChatConfig -ConfigFile $configFile -Mode "relay" -Name $deviceName -RelayUrl $(if ($snapshot.RelayUrl) { $snapshot.RelayUrl } else { $RELAY_WS_URL }) -ChannelId $snapshot.ChannelId -Secret $snapshot.Secret
        Consume-GoChatModeSwitchAuthorization -ConfigFile $configFile
        Write-Info "Existing relay credentials found. Keeping channelId=$($snapshot.ChannelId)"
        Print-RelayStatus $configFile
        return
    }

    $registered = Try-RegisterRelay -DeviceName $deviceName
    if (-not $registered) {
        Exit-WithError "Relay registration did not return usable credentials. The extension was installed, but relay setup is incomplete. Check network access to $RELAY_HTTP_URL or install again with -Code."
    }

    Write-GoChatConfig -ConfigFile $configFile -Mode "relay" -Name $registered.Name -RelayUrl $registered.RelayUrl -ChannelId $registered.ChannelId -Secret $registered.Secret
    Consume-GoChatModeSwitchAuthorization -ConfigFile $configFile
    Write-Ok "Relay registered. channelId=$($registered.ChannelId)"
    Print-RelayStatus $configFile
}

function Configure-Local {
    $configFile = Join-Path (Get-OpenClawDir) "openclaw.json"
    Require-GoChatModeSwitchAuthorization -ConfigFile $configFile -TargetMode "local"
    $snapshot = Get-GoChatConfigSnapshot $configFile
    $deviceName = if ($snapshot.Name) { $snapshot.Name } else { Get-DefaultDeviceName }
    $secret = if ($snapshot.Secret) { $snapshot.Secret } else { New-RandomSecret }
    $port = if ($snapshot.DirectPort) { $snapshot.DirectPort } else { "$DEFAULT_LOCAL_PORT" }

    Write-GoChatConfig -ConfigFile $configFile -Mode "local" -Name $deviceName -Secret $secret -DirectPort $port
    Consume-GoChatModeSwitchAuthorization -ConfigFile $configFile
    Print-LocalStatus $configFile
}

function Install-Extension {
    if ($FromTarball) {
        if (-not (Test-Path $FromTarball)) {
            Exit-WithError "Tarball not found: $FromTarball"
        }
        Install-FromTarball $FromTarball
        return
    }

    $scriptDir = $PSScriptRoot
    if ($scriptDir -and (Test-Path (Join-Path $scriptDir "package.json"))) {
        Install-FromSource $scriptDir
        return
    }

    Install-Remote
}

function Show-Help {
    Write-Host ""
    Write-Host "Usage: .\install.ps1 [OPTIONS]"
    Write-Host ""
    Write-Host "Options:"
    Write-Host "  -Relay             Relay mode (default)"
    Write-Host "  -Local             Local mode"
    Write-Host "  -Code <code>       Claim a 6-digit relay connection code"
    Write-Host "  -Mode <mode>       Set mode: relay or local"
    Write-Host "  -FromTarball <p>   Install from a local .tar.gz"
    Write-Host "  -Help              Show this help"
    Write-Host ""
    Write-Host "Remote install (run in PowerShell):"
    Write-Host "  & ([scriptblock]::Create((irm '$REMOTE_INSTALL_PS_URL')))"
    Write-Host "  & ([scriptblock]::Create((irm '$REMOTE_INSTALL_PS_URL'))) -Code '123456'"
    Write-Host ""
}

function Main {
    Write-Host ""
    Write-Host "`e[34;1m─────────────────────────────────────"
    Write-Host "  GoChat Extension Installer  v$VERSION"
    Write-Host "─────────────────────────────────────`e[0m"
    Write-Host ""

    if ($Help) {
        Show-Help
        exit 0
    }

    Detect-Platform
    Ensure-Prerequisites
    Install-Extension

    $installMode = "relay"
    if ($Local) { $installMode = "local" }
    if ($Relay) { $installMode = "relay" }
    if ($Mode -in @("relay", "local")) { $installMode = $Mode }
    if ($Code) { $installMode = "relay" }

    Write-Host ""
    Write-Info "──── GoChat Configuration ────"
    Write-Info "  platform:      $($Script:Platform) ($($Script:Arch))"
    Write-Info "  mode:          $installMode"
    Write-Info "  extension dir: $(Join-Path (Get-ExtensionsDir) $EXTENSION_NAME)"
    if ($installMode -eq "relay") {
        Write-Info "  relay:         $RELAY_WS_URL"
        Write-Info "  dmPolicy:      open"
        if ($Code) {
            Write-Info "  connectCode:   $Code"
        }
    } else {
        Write-Info "  server:        built-in HTTP API on port $DEFAULT_LOCAL_PORT"
        Write-Info "  dmPolicy:      open"
    }
    Write-Info "──────────────────────────"

    if ($installMode -eq "local") {
        Configure-Local
    } else {
        Configure-Relay $Code
    }

    Write-Host ""
    Write-Ok "GoChat is ready!"
    Write-Host ""
    Write-Info "Next steps:"
    Write-Info "  openclaw gateway run"
    Write-Info "  openclaw channels list"
    Write-Info "  openclaw gochat show-credentials"
    Write-Host ""
}

Main
