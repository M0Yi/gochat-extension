# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
