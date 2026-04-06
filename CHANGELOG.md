# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
