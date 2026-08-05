# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.3.5] - 2026-08-04

### Added
- New `--incoming PATH` / `--incoming=PATH` option to import Takeout ZIP files
  from any directory instead of the default `Incoming/` folder.

## [v0.3.4] - 2026-08-04

### Fixed
- Replaced the `archive/zip` fallback with a **local-header scan** that does
  not depend on the standard library's stricter parser. This handles archives
  that Mint/unzip can open but Go cannot.
- A file that cannot be appended (even after fallback) is now logged as a
  warning instead of aborting the entire sync.

## [v0.3.3] - 2026-08-04

### Fixed
- Added a fallback path when copying an incoming entry by raw compressed stream
  fails. Some large Google Takeout archives report local-header offsets that
  cannot be used directly; the engine now re-reads the entry through the
  standard library and stores it uncompressed. Content is preserved.

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
