# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.3.2] - 2026-08-04

### Fixed
- Installer downloads the wrong version: `install.sh` / `install.ps1` had a
  hardcoded default version that fell behind the release they shipped in.
  The Release workflow now templates the actual tag into the installers at
  build time, so `releases/download/vX.Y.Z/install.sh` always installs `vX.Y.Z`.

## [v0.3.1] - 2026-08-04

### Fixed
- Panic when running `TakeOutBack.sh` with no subcommand (`slice bounds out of range [1:0]`).
  The argument parser now safely handles an empty argument list.

## [v0.3.0] - 2026-08-04

### Added
- Initial working release of the TakeOutBack consolidation engine.
- Append-only ZIP archive strategy with hand-written central directory and ZIP64 support.
- Cross-platform static Go binaries for Linux x86_64 and Windows x86_64.
- Synchronization command that detects new, modified and unchanged files using path, size and CRC32 metadata.
- Versioning scheme preserving historical files as `name__vN.ext`.
- Crash recovery via sidecar state file and full local-header scan fallback.
- `verify` command with optional deep CRC check.
- `stats` command showing archive size, entry count and compression ratio.
- `compact` command to rewrite the archive and remove dead central-directory blocks.
- `update` command to download newer binaries from GitHub Releases.
- One-shot installers for Linux (`install.sh`) and Windows (`install.ps1`).
- Launcher scripts `TakeOutBack.sh` and `TakeOutBack.bat`.
- Architecture, installation, usage, development and troubleshooting documentation.

### Changed
- N/A

### Fixed
- N/A

### Removed
- N/A

## [v0.2.0] - 2026-08-04

### Added
- Project skeleton and architecture document.

## [v0.1.0] - 2026-08-04

### Added
- Initial repository layout.
