# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2026.4.9-plugin.26] - 2026-04-09

### Fixed
- Shell installer now marks `gochat` as trusted even when managed `openclaw plugins install` falls back to direct file extraction, so `plugins.allow is empty` and `untracked local code` warnings do not appear after fallback installs
- PowerShell installer now writes the same `plugins.allow` trust entry after Windows installs, matching the shell installer trust behavior

## [2026.4.8-plugin.25] - 2026-04-08

### Added
- GoChat now proactively pushes the current subagent permission state into each conversation on first contact and whenever the local gateway pairing state changes, so users can see `ready` / `action required` status without asking first
- Pending local gateway repair requests now surface as an actionable status message with `openclaw devices approve ...`, while recovered sessions push a `ready` status message after admin scope returns

## [2026.4.8-plugin.24] - 2026-04-08

### Added
- When an inbound GoChat session fails with gateway `pairing required`, the plugin now sends an actionable chat reply that includes a concrete `openclaw devices approve ...` command and a fallback `openclaw pairing approve --channel gochat <PAIRING_CODE> --notify` hint instead of letting the model narrate a vague recovery attempt

## [2026.4.8-plugin.23] - 2026-04-08

### Changed
- Default installs no longer run the experimental automatic local gateway authorization/bootstrap path during install or plugin startup
- Existing accounts now require an explicit one-time `openclaw gochat authorize-mode-switch --mode <local|relay>` grant before switching between local and relay modes

### Added
- Added `openclaw gochat authorize-mode-switch` to create a short-lived one-time mode-switch authorization consumed by the next successful switch

## [2026.4.8-plugin.22] - 2026-04-08

### Fixed
- Shell installer now uses `npm install --omit=dev` everywhere instead of the deprecated `--production` flag, so modern npm no longer prints the `production Use --omit=dev instead` warning during install
- README manual-install examples now use `npm install --omit=dev` to match the shipped installers

## [2026.4.8-plugin.21] - 2026-04-08

### Fixed
- Installers now probe `openclaw gochat --help` before calling `ensure-gateway-access`, so older or partially-loaded CLI states no longer print `too many arguments for 'gochat'` during installation
- Shell and PowerShell installers now suppress bootstrap command parser noise and fall back cleanly to the plugin runtime retry path when the local gateway access command is unavailable

## [2026.4.8-plugin.20] - 2026-04-08

### Fixed
- Relay runtime now keeps a short-lived local gateway access watch active while GoChat is executing work, so safe local CLI repair requests that appear mid-session can still be auto-approved instead of only being checked during install or plugin startup
- Relay runtime now retries the same safe gateway access bootstrap after relay-side errors, reducing `pairing required` interruptions on older local OpenClaw builds that still surface loopback scope-upgrade repair windows during subagent or operator actions

## [2026.4.8-plugin.19] - 2026-04-08

### Added
- Added `openclaw gochat ensure-gateway-access` to normalize loopback gateway URLs and auto-approve safe local CLI repair requests that only ask for the standard full operator scopes

### Changed
- Installers now persist GoChat gateway access bootstrap defaults and run the local gateway access bootstrap step right after configuration
- Plugin startup now retries the same safe local gateway access bootstrap in the background so existing installs can recover once the gateway is live

## [2026.4.8-plugin.18] - 2026-04-08

### Fixed
- Shell installer now creates the base OpenClaw config before relay registration so fresh installs can save pairing credentials immediately instead of waiting for first gateway startup
- Shell installer now retries relay registration and preserves the configured device name when requesting new relay credentials

## [2026.4.8-plugin.17] - 2026-04-08

### Added
- Added plugin-side runtime refresh handling so the server can request an immediate parameter/status resync after model or runtime parameter changes
- Added runtime schema version metadata for the standardized runtime parameter sync protocol

### Changed
- Relay runtime status snapshots now reload the latest OpenClaw config before reporting current model and account metadata

## [2026.4.8-plugin.16] - 2026-04-08

### Added
- Relay status metadata now includes the current model plus the active OpenClaw command and arguments
- GoChat app header now shows the current model and runtime command details from the connected plugin

## [2026.4.7-plugin.15] - 2026-04-07

### Fixed
- Script installs now prefer `openclaw plugins install` for managed plugin provenance when the OpenClaw CLI is available
- Script installs now auto-add `gochat` to `plugins.allow` so trusted installs do not require manual trust configuration

## [2026.4.7-plugin.14] - 2026-04-07

### Fixed
- Fixed relay WebSocket message handling so long-running AI execution and block streaming no longer block heartbeat/status traffic
- Added active-job relay status keepalive pulses during long-running executions
- Fixed S3 presigned upload signing for browser uploads, including UTF-8 filenames and required `x-amz-content-sha256` propagation
- Added stricter S3 upload test flow: upload, public access verification, then cleanup

## [2026.4.7-plugin.13] - 2026-04-07

### Changed
- Shell installer now refuses npm fallback so script installs always come from current GitHub source
- Enabled GoChat block streaming by default across plugin runtime and installers
- Added relay reply event passthrough and chat UI incremental rendering support
- Version bump to plugin.13

## [2026.4.6-plugin.12] - 2026-04-07

### Changed
- Enabled GoChat block streaming by default across plugin runtime and installers
- Added relay reply event passthrough and chat UI incremental rendering support
- Version bump to plugin.12

## [2026.4.6-plugin.9] - 2026-04-06

### Changed
- Version bump to plugin.9

## [2026.3.28] - 2026-03-28

### Added
- Simplified configuration model: `local` and `relay` modes only
- Zero-config local mode with auto-generated secrets
- Full configuration logging on startup
- New `--local` / `--relay` installer flags
- `--from-tarball` option for offline installation

### Changed
- `direct` mode renamed to `local` mode
- `relay-ws` mode renamed to `relay` mode
- Removed `baseUrl`, `callbackUrl`, `callbackSecret` configuration options
- Removed HTTP webhook server (WebSocket relay only)
- `dmPolicy` default changed from `pairing` to `open`

### Fixed
- Improved auto-reconnection for relay mode
- Better error messages for misconfiguration

## [2026.3.x] - Previous Versions

- See git history for previous changelog entries
