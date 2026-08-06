# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.4.3] - 2026-08-06

### Added
- `sync` now shows progress bars for the backup of the current consolidated
  archive and for copying existing entries into the new consolidated archive.
- `sync` now prints the number of loaded existing entries before processing.
- The archive list is printed earlier, before any heavy I/O starts.

### Changed
- N/A

### Fixed
- N/A

## [v0.4.2] - 2026-08-06

### Changed
- Removed all disk-space estimation, warnings and checks before `sync`.
- Removed `--yes` and `--force` flags; `sync` now runs immediately when invoked.
- The confirmation prompt is no longer shown; the backup starts right after the
  archive list.

## [v0.4.1] - 2026-08-06

### Added
- New `--archive-dir PATH` option to use a custom archive directory.
- New `--force` flag to run `sync` even when the disk-space estimate reports
  insufficient space.
- The sync plan now displays the configured incoming, archive, backup and temp
  directories so the user can verify which paths are used.

### Changed
- Disk-space estimation is now computed per filesystem and accounts for the fact
  that temporary files are renamed into place (not copied), and that incoming ZIP
  files are only read, not written. This removes the previous 4x over-estimation.

### Fixed
- `sync` options such as `--force` and `--yes` are now accepted before or after
  the `sync` command.

## [v0.4.0] - 2026-08-06

### Added
- `sync` now prints a disk-space plan before writing anything and asks for
  confirmation. The plan estimates the peak space required on each affected disk,
  taking into account the existing consolidated archive, incoming ZIP volume,
  existing `Added-*.zip` archives, a worst-case new Added archive and the backup
  copy. Use `--yes` to skip the prompt.
- New `--temp-dir PATH` option to use a custom temporary work directory.
- New `--backup-dir PATH` option to store backup copies of the consolidated
  archive in a custom directory.
- New `disk_unix.go` and `disk_windows.go` helpers to read free filesystem space.

### Changed
- The default command is no longer `sync`. Running `takeoutback` without a
  command now prints the help message.
- The initial import no longer creates an `Added-*.zip` archive. Added archives are
  produced only for subsequent imports.

### Fixed
- N/A

## [v0.3.9] - 2026-08-05

### Added
- Per-execution temporary directory under `TakeOutBack/temp/run-YYYYMMDD-HHMMSS.mmm/`.
  All scratch work now happens there instead of inside `Archive/`.
- Automatic cleanup of leftover `run-*` directories from interrupted runs and
  leftover `*.tmp` / `*.rebuild` / `*.compact` files in `Archive/` at startup.
- The per-execution temp directory is removed after a successful run.

### Changed
- Archive timestamps now use the system's local time instead of UTC.
- The temporary consolidated and added archives are written under the
  per-execution temp directory and renamed into `Archive/` only at the end.

### Fixed
- `Archive/` no longer accumulates partial `.tmp` files; only final
  `Consolidated-*.zip`, `Added-*.zip`, and sidecar files remain.

## [v0.3.8] - 2026-08-04

### Added
- `Backup/` directory: the previous consolidated archive is copied here before
  each sync is allowed to replace it. The last 5 backups are retained.
- Timestamped archive names: consolidated archives are now named
  `Consolidated-YYYYMMDD-HHMMSS.mmm.zip` and a companion `Added-*.zip` is created
  containing only the files imported during that sync.
- Stale lock detection: if a sync is interrupted (Ctrl+C, power loss, drive
  removal), the next run detects the abandoned `Archive/.consolidated.lock` and
  resumes safely.

### Changed
- The consolidated archive is now rebuilt from a temporary file on every sync.
  The previous archive is never modified in place, making interruption safe.
- Updated all documentation to describe the new archive layout, backup
  behavior, stale-lock recovery and timestamped output.

### Fixed
- Removed `Archive/Consolidated.zip` and `Archive/Consolidated.zip.state.json`
  in favor of `Archive/Consolidated-*.zip` and `Archive/state.json`.

## [v0.3.7] - 2026-08-04

### Added
- Live sync progress: before processing, TakeOutBack prints a numbered list of
  archives to process, then redraws a per-archive ASCII progress bar showing the
  current entry count and percentage.

### Changed
- Updated all documentation (`README.md`, `docs/*.md`) to describe the live
  progress output and the Windows path-quoting fix.

## [v0.3.6] - 2026-08-04

### Fixed
- Windows batch launcher: avoid `"F:\\"` escaping the closing quote and
  corrupting the `--root` path by appending `.` to the directory argument.
- Use `path` (forward slashes) for ZIP entry name manipulation so file-version
  suffixes and skip-name patterns behave identically on Linux and Windows.

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
