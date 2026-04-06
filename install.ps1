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

# ──────────────────────────────────────────────
# GoChat Extension Installer for OpenClaw
# Supports: Windows (native), PowerShell 5.1+
# ──────────────────────────────────────────────

$ErrorActionPreference = "Stop"
$VERSION = "2026.4.6-plugin.9"
$EXTENSION_NAME = "gochat"
$PACKAGE_NAME = "@m0yi/gochat"
$OPENCLAW_MIN_VERSION = "2026.3.28"
$REPO_URL = "https://github.com/M0Yi/gochat-extension.git"
$REMOTE_INSTALL_PS_URL = "https://raw.githubusercontent.com/M0Yi/gochat-extension/main/extensions/gochat/install.ps1"
$DEFAULT_RELAY_HTTP_URL = "https://fund.moyi.vip"
$DEFAULT_RELAY_WS_URL = "wss://fund.moyi.vip/ws/plugin"
$RELAY_HTTP_URL = if ($env:GOCHAT_RELAY_HTTP_URL) { $env:GOCHAT_RELAY_HTTP_URL } else { $DEFAULT_RELAY_HTTP_URL }
$RELAY_WS_URL = if ($env:GOCHAT_RELAY_WS_URL) { $env:GOCHAT_RELAY_WS_URL } else { $DEFAULT_RELAY_WS_URL }

$Script:Platform = ""
$Script:Arch = ""
$Script:OpenClawBin = ""
$Script:NodeVersion = ""

# ──────────────────────────────────────────────
# Logging
# ──────────────────────────────────────────────

function Write-Info($msg)  { Write-Host "`e[36;1m[gochat]`e[0m $msg" }
function Write-Ok($msg)    { Write-Host "`e[32;1m[gochat]`e[0m $msg" }
function Write-Warn($msg)  { Write-Host "`e[33;1m[gochat]`e[0m $msg" }
function Write-Fail($msg)  { Write-Host "`e[31;1m[gochat]`e[0m $msg" }

# ──────────────────────────────────────────────
# JSON helper via Node.js
# ──────────────────────────────────────────────

function Get-JsonValue {
    param([string]$JsonData, [string]$Key)
    try {
        $result = & node -e "const a=process.argv.slice(1),k=a[0],d=JSON.parse(a[1]);let v=d;for(const s of k.split('.')){if(v==null||v[s]===undefined){v=null;break}v=v[s]}if(v!==null&&v!==undefined)process.stdout.write(String(v))" $Key $JsonData 2>$null
        return $result
    } catch {
        return ""
    }
}

# ──────────────────────────────────────────────
# OS & Architecture Detection
# ──────────────────────────────────────────────

function Detect-Platform {
    $Script:Platform = "windows"

    $cpu = $env:PROCESSOR_ARCHITECTURE
    if ($cpu -match "AMD64|X64") { $Script:Arch = "amd64" }
    elseif ($cpu -match "ARM64") { $Script:Arch = "arm64" }
    else { $Script:Arch = "unknown" }

    Write-Info "Platform: $($Script:Platform) ($($Script:Arch))"
}

# ──────────────────────────────────────────────
# Detect OpenClaw
# ──────────────────────────────────────────────

function Detect-OpenClaw {
    $oc = Get-Command "openclaw" -ErrorAction SilentlyContinue
    if ($oc) {
        $Script:OpenClawBin = $oc.Source
        Write-Info "Found OpenClaw at: $($Script:OpenClawBin)"
        return $true
    }

    $searchPaths = @(
        "$env:USERPROFILE\.local\bin\openclaw.exe",
        "$env:USERPROFILE\.local\bin\openclaw.cmd",
        "$env:APPDATA\npm\openclaw.cmd",
        "$env:APPDATA\npm\openclaw.ps1",
        "${env:ProgramFiles}\openclaw\bin\openclaw.exe",
        "${env:LocalAppData}\openclaw\bin\openclaw.exe"
    )

    foreach ($p in $searchPaths) {
        if (Test-Path $p) {
            $Script:OpenClawBin = $p
            Write-Info "Found OpenClaw at: $p"
            return $true
        }
    }

    $npmPrefix = Get-Command "npm" -ErrorAction SilentlyContinue
    if ($npmPrefix) {
        $prefix = & npm config get prefix 2>$null
        if ($prefix) {
            $npmOc = Join-Path $prefix "openclaw.cmd"
            if (Test-Path $npmOc) {
                $Script:OpenClawBin = $npmOc
                Write-Info "Found OpenClaw at: $npmOc"
                return $true
            }
        }
    }

    return $false
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

# ──────────────────────────────────────────────
# Ensure directory is writable
# ──────────────────────────────────────────────

function Ensure-DirWritable {
    param([string]$TargetDir)
    try {
        if (-not (Test-Path $TargetDir)) {
            New-Item -ItemType Directory -Path $TargetDir -Force | Out-Null
        }
        $testFile = Join-Path $TargetDir ".write-test-$([guid]::NewGuid().ToString('n').Substring(0,8))"
        Set-Content -Path $testFile -Value "test" -ErrorAction Stop
        Remove-Item $testFile -Force
    } catch {
        Write-Fail "Directory not writable: $TargetDir"
        Write-Fail "Try running as Administrator or check permissions."
        exit 1
    }
}

# ──────────────────────────────────────────────
# Install functions
# ──────────────────────────────────────────────

function Install-FromTarball {
    param([string]$Tarball)

    $extDir = Get-ExtensionsDir
    Ensure-DirWritable $extDir

    $target = Join-Path $extDir $EXTENSION_NAME
    if (Test-Path $target) {
        Write-Info "Removing previous installation..."
        Remove-Item $target -Recurse -Force
    }

    Write-Info "Extracting to $target..."

    $tmpExtract = Join-Path $env:TEMP "gochat-extract-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpExtract -Force | Out-Null

    tar -xzf $Tarball -C $tmpExtract 2>$null
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Failed to extract tarball."
        Remove-Item $tmpExtract -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }

    $extracted = Get-ChildItem $tmpExtract -Directory | Select-Object -First 1
    if ($extracted) {
        Move-Item $extracted.FullName $target -Force
    } else {
        Move-Item $tmpExtract $target -Force
    }

    $pkgJson = Join-Path $target "package.json"
    if (Test-Path $pkgJson) {
        Write-Info "Installing npm dependencies..."
        Push-Location $target
        try {
            & npm install --production 2>&1 | ForEach-Object { Write-Verbose $_ }
        } catch {
            Write-Warn "npm install had warnings (non-fatal)"
        }
        Pop-Location
    }

    Write-Ok "Installed to $target"
}

function Install-FromSource {
    param([string]$SourceDir)

    $extDir = Get-ExtensionsDir
    Ensure-DirWritable $extDir

    $target = Join-Path $extDir $EXTENSION_NAME
    if (Test-Path $target) {
        Write-Info "Removing previous installation..."
        Remove-Item $target -Recurse -Force
    }

    Write-Info "Copying to $target..."
    Copy-Item $SourceDir $target -Recurse -Force

    $nodeModules = Join-Path $target "node_modules"
    if (Test-Path $nodeModules) { Remove-Item $nodeModules -Recurse -Force -ErrorAction SilentlyContinue }
    $gitDir = Join-Path $target ".git"
    if (Test-Path $gitDir) { Remove-Item $gitDir -Recurse -Force -ErrorAction SilentlyContinue }

    $pkgJson = Join-Path $target "package.json"
    if (Test-Path $pkgJson) {
        Write-Info "Installing npm dependencies..."
        Push-Location $target
        try {
            & npm install --production 2>&1 | ForEach-Object { Write-Verbose $_ }
        } catch {
            Write-Warn "npm install had warnings (non-fatal)"
        }
        Pop-Location
    }

    Write-Ok "Installed to $target"
}

function Install-FromGit {
    $tmpDir = Join-Path $env:TEMP "gochat-install-$(Get-Random)"

    Write-Info "Cloning from $REPO_URL..."
    $git = Get-Command "git" -ErrorAction SilentlyContinue
    if (-not $git) {
        Write-Fail "git is required but not found."
        Write-Fail "Install git: https://git-scm.com/download/win"
        exit 1
    }

    & git clone --depth 1 $REPO_URL $tmpDir 2>&1 | ForEach-Object { Write-Verbose $_ }
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "git clone failed. Check network connection."
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }

    Install-FromSource $tmpDir
    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}

function Install-FromNpmPack {
    $tmpDir = Join-Path $env:TEMP "gochat-pack-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    Write-Info "Downloading package from npm: $PACKAGE_NAME"
    Push-Location $tmpDir
    try {
        $tarballName = (& npm pack $PACKAGE_NAME --silent 2>$null | Select-Object -Last 1).Trim()
        if (-not $tarballName) {
            throw "npm pack returned no tarball"
        }

        $tarballPath = Join-Path $tmpDir $tarballName
        if (-not (Test-Path $tarballPath)) {
            throw "tarball not found after npm pack: $tarballPath"
        }

        Install-FromTarball $tarballPath
    } catch {
        Write-Fail "npm package install failed. Check npm registry access."
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    } finally {
        Pop-Location
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Install-Remote {
    $git = Get-Command "git" -ErrorAction SilentlyContinue
    if ($git) {
        Install-FromGit
        return
    }

    Write-Warn "git not found. Falling back to npm package install..."
    Install-FromNpmPack
}

# ──────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────

function Ensure-ConfigFile {
    param([string]$ConfigFile)
    $dir = Split-Path $ConfigFile -Parent
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    if (-not (Test-Path $ConfigFile)) {
        Set-Content -Path $ConfigFile -Value "{`n}"
    }
}

function Write-ConfigWithNode {
    param(
        [string]$ConfigFile,
        [string]$ChannelId,
        [string]$Secret,
        [string]$RelayUrl,
        [string]$Name = ""
    )

    $nameArg = if ($Name) { $Name } else { "OpenClaw GoChat Plugin" }
    $relayArg = if ($RelayUrl) { $RelayUrl } else { $RELAY_WS_URL }

    & node -e @"
        const fs = require('fs');
        const configFile = process.argv[1];
        const channelId = process.argv[2];
        const secret = process.argv[3];
        const relayUrl = process.argv[4];
        const name = process.argv[5];
        let c = {};
        try { c = JSON.parse(fs.readFileSync(configFile, 'utf8')); } catch {}
        if (!c.channels) c.channels = {};
        c.channels.gochat = Object.assign(c.channels.gochat || {}, {
            enabled: true,
            mode: 'relay',
            name: name,
            channelId: channelId,
            webhookSecret: secret,
            relayPlatformUrl: relayUrl,
            dmPolicy: 'open'
        });
        fs.writeFileSync(configFile, JSON.stringify(c, null, 2) + '\n');
"@ $ConfigFile $ChannelId $Secret $relayArg $nameArg 2>$null

    if ($LASTEXITCODE -ne 0) {
        throw "Failed to write config"
    }
}

function Claim-RelayPairCode {
    param([string]$ConfigFile, [string]$PairCode)

    Ensure-ConfigFile $ConfigFile

    Write-Info "Claiming connection code $PairCode..."
    try {
        $body = @{ code = $PairCode; name = "OpenClaw GoChat Plugin" } | ConvertTo-Json -Compress
        $response = Invoke-RestMethod -Uri "$RELAY_HTTP_URL/api/plugin/pair/claim" `
            -Method POST `
            -ContentType "application/json" `
            -Body $body `
            -TimeoutSec 20 `
            -ErrorAction Stop
    } catch {
        Write-Fail "Failed to claim connection code. Check the code and network, then try again."
        exit 1
    }

    $regChannelId = Get-JsonValue ($response | ConvertTo-Json -Depth 10 -Compress) "channelId"
    $regSecret = Get-JsonValue ($response | ConvertTo-Json -Depth 10 -Compress) "secret"
    $regName = Get-JsonValue ($response | ConvertTo-Json -Depth 10 -Compress) "name"
    $regRelayUrl = Get-JsonValue ($response | ConvertTo-Json -Depth 10 -Compress) "relayPlatformUrl"

    if (-not $regChannelId -or -not $regSecret) {
        Write-Fail "Connection code response missing channelId or secret."
        exit 1
    }

    if (-not $regRelayUrl) { $regRelayUrl = $RELAY_WS_URL }

    Write-Ok "Connection code accepted. channelId=$regChannelId"

    Write-Info "Writing config..."
    try {
        Write-ConfigWithNode -ConfigFile $ConfigFile -ChannelId $regChannelId -Secret $regSecret -RelayUrl $regRelayUrl -Name $regName
        Write-Ok "Config saved."
    } catch {
        Write-Fail "Failed to write config."
        exit 1
    }

    Print-Credentials
}

function Register-Relay {
    param([string]$PairCode = "")

    $ocDir = Get-OpenClawDir
    $configFile = Join-Path $ocDir "openclaw.json"

    if ($PairCode) {
        Claim-RelayPairCode $configFile $PairCode
        return
    }

    if (-not (Test-Path $configFile)) {
        Write-Warn "Config not found ($configFile). Will register on first gateway start."
        return
    }

    $configRaw = Get-Content $configFile -Raw
    $existingId = Get-JsonValue $configRaw "channels.gochat.channelId"

    if ($existingId) {
        Write-Info "Existing channelId: $existingId -- skipping registration."
        Ensure-DmPolicyOpen $configFile
        Print-Credentials
        return
    }

    Write-Info "Registering with relay platform..."
    try {
        $body = '{"name":"OpenClaw GoChat Plugin"}'
        $response = Invoke-RestMethod -Uri "$RELAY_HTTP_URL/api/plugin/register" `
            -Method POST `
            -ContentType "application/json" `
            -Body $body `
            -TimeoutSec 15 `
            -ErrorAction Stop
    } catch {
        Write-Warn "Registration failed (network error). Will auto-register on first gateway start."
        return
    }

    $respJson = $response | ConvertTo-Json -Depth 10 -Compress
    $regChannelId = Get-JsonValue $respJson "channelId"
    $regSecret = Get-JsonValue $respJson "secret"

    if (-not $regChannelId -or -not $regSecret) {
        Write-Warn "Registration response missing channelId or secret."
        return
    }

    Write-Ok "Registered! channelId=$regChannelId"

    Write-Info "Writing config..."
    try {
        Write-ConfigWithNode -ConfigFile $configFile -ChannelId $regChannelId -Secret $regSecret
        Write-Ok "Config saved."
    } catch {
        Write-Warn "Failed to write config."
        return
    }

    Print-Credentials
}

function Ensure-DmPolicyOpen {
    param([string]$ConfigFile)
    & node -e @"
        const fs = require('fs');
        const c = JSON.parse(fs.readFileSync(process.argv[1],'utf8'));
        const g = c.channels && c.channels.gochat;
        if (g && g.dmPolicy !== 'open') {
            g.dmPolicy = 'open';
            fs.writeFileSync(process.argv[1], JSON.stringify(c, null, 2) + '\n');
        }
"@ $ConfigFile 2>$null
}

function Print-Credentials {
    $ocDir = Get-OpenClawDir
    $configFile = Join-Path $ocDir "openclaw.json"

    if (-not (Test-Path $configFile)) { return }

    $configRaw = Get-Content $configFile -Raw
    $channelId = Get-JsonValue $configRaw "channels.gochat.channelId"
    $secret = Get-JsonValue $configRaw "channels.gochat.webhookSecret"

    if (-not $channelId -and -not $secret) { return }

    Write-Host ""
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[36;1m  GoChat Connection Credentials`e[0m"
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host ""

    if ($channelId) {
        Write-Host "  Channel ID:    `e[32m$channelId`e[0m"
    } else {
        Write-Host "  Channel ID:    (pending - will be registered on first gateway start)"
    }

    if ($secret) {
        Write-Host "  Secret Key:    `e[32m$secret`e[0m"
    } else {
        Write-Host "  Secret Key:    (pending - will be generated on first gateway start)"
    }

    Write-Host "  DM Policy:     open (no pairing approval needed)"
    Write-Host ""
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host ""
}

function Configure-GoChat {
    param([string]$InstallMode, [string]$PairCode = "")

    $extDir = Get-ExtensionsDir

    Write-Host ""
    Write-Info "──── GoChat Configuration ────"
    Write-Info "  platform:      $($Script:Platform) ($($Script:Arch))"
    Write-Info "  mode:          $InstallMode"
    Write-Info "  extension dir: $extDir\$EXTENSION_NAME"

    if ($InstallMode -eq "relay") {
        Write-Info "  relay:         $RELAY_WS_URL"
        Write-Info "  dmPolicy:      open (skip pairing)"
        if ($PairCode) {
            Write-Info "  connectCode:   $PairCode"
        }
        Write-Info "──────────────────────────"
        Write-Host ""
        Register-Relay $PairCode
    } else {
        Write-Info "  server:        built-in HTTP API on port 9750"
        Write-Info "  dmPolicy:      open"
        Write-Info "──────────────────────────"
    }

    Write-Host ""
    Write-Ok "GoChat is ready!"
    Write-Host ""
    Write-Info "Usage:"
    Write-Info "  openclaw gateway run                # Start gateway"
    Write-Info "  openclaw channels list              # Check channel status"
    Write-Info "  openclaw gochat show-credentials    # View credentials"
    Write-Host ""
}

# ──────────────────────────────────────────────
# Help
# ──────────────────────────────────────────────

function Show-Help {
    Write-Host ""
    Write-Host "Usage: .\install.ps1 [OPTIONS]"
    Write-Host ""
    Write-Host "Options:"
    Write-Host "  -Relay             Relay mode (default, auto-register)"
    Write-Host "  -Local             Local mode"
    Write-Host "  -Code <code>       Claim a 6-digit relay connection code"
    Write-Host "  -Mode <mode>       Set mode: local or relay"
    Write-Host "  -FromTarball <path> Install from .tar.gz"
    Write-Host "  -Help              Show this help"
    Write-Host ""
    Write-Host "Examples:"
    Write-Host "  .\install.ps1"
    Write-Host "  .\install.ps1 -Code 123456"
    Write-Host "  .\install.ps1 -Local"
    Write-Host "  .\install.ps1 -FromTarball .\gochat-extension.tar.gz"
    Write-Host ""
    Write-Host "Remote install (run in PowerShell):"
    Write-Host "  & ([scriptblock]::Create((irm '$REMOTE_INSTALL_PS_URL')))"
    Write-Host "  & ([scriptblock]::Create((irm '$REMOTE_INSTALL_PS_URL'))) -Code '123456'"
    Write-Host ""
}

# ──────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────

function Main {
    Write-Host ""
    Write-Host "`e[34;1m─────────────────────────────────────"
    Write-Host "  GoChat Extension Installer  v$VERSION"
    Write-Host "─────────────────────────────────────`e[0m"
    Write-Host ""

    if ($Help) { Show-Help; exit 0 }

    Detect-Platform

    # Resolve mode
    $installMode = "relay"
    if ($Local) { $installMode = "local" }
    if ($Relay) { $installMode = "relay" }
    if ($Mode -in @("local", "relay")) { $installMode = $Mode }
    if ($Code) { $installMode = "relay" }

    # Check Node.js
    $node = Get-Command "node" -ErrorAction SilentlyContinue
    if (-not $node) {
        Write-Fail "Node.js is required but not found."
        Write-Fail "Install: https://nodejs.org/  or  winget install OpenJS.NodeJS.LTS"
        exit 1
    }

    $npm = Get-Command "npm" -ErrorAction SilentlyContinue
    if (-not $npm) {
        Write-Fail "npm is required but not found."
        Write-Fail "Reinstall Node.js: https://nodejs.org/"
        exit 1
    }

    $Script:NodeVersion = & node --version 2>$null
    Write-Info "Node.js: $($Script:NodeVersion)"

    $ocFound = Detect-OpenClaw
    if ($ocFound) {
        $ocVer = & $Script:OpenClawBin --version 2>$null | Select-Object -First 1
        Write-Info "OpenClaw version: $ocVer"
    } else {
        Write-Warn "OpenClaw CLI not found. Extension will install but won't work until OpenClaw is installed."
    }

    # Install
    if ($FromTarball) {
        if (-not (Test-Path $FromTarball)) {
            Write-Fail "Tarball not found: $FromTarball"
            exit 1
        }
        Install-FromTarball $FromTarball
    } else {
        $scriptDir = $PSScriptRoot
        if ($scriptDir -and (Test-Path (Join-Path $scriptDir "package.json"))) {
            Install-FromSource $scriptDir
        } else {
            Install-Remote
        }
    }

    Configure-GoChat $installMode $Code

    # Environment Summary
    Write-Host ""
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "`e[36;1m  Environment Summary`e[0m"
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host "  Plugin:        GoChat v$VERSION"
    Write-Host "  Platform:      $($Script:Platform) ($($Script:Arch))"
    Write-Host "  Node.js:       $($Script:NodeVersion)"
    if ($Script:OpenClawBin) {
        $ocVer = & $Script:OpenClawBin --version 2>$null | Select-Object -First 1
        Write-Host "  OpenClaw:      $ocVer"
    } else {
        Write-Host "  OpenClaw:      (not installed)"
    }
    Write-Host "`e[36;1m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━`e[0m"
    Write-Host ""

    Write-Host "`e[32;1mGoChat extension installed successfully!`e[0m"
}

Main
