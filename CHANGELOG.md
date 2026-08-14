# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.5.9] - 2026-08-14

### Added
- Dedicated daily error log: warnings and errors are now written to both the
  general log (`YYYY-MM-DD.log`) and a separate `YYYY-MM-DD-errors.log` file.
- ETA, throughput and adaptive total-time estimates in all byte-based progress
  bars (backup, sync, safe mode storage uploads).
- Backup interruption: pressing Esc or Ctrl+C during the backup phase asks for
  confirmation and, if confirmed, aborts cleanly while leaving the original
  archive untouched.

### Changed
- Interactive sync setup now asks whether to create a backup *before* asking for
  the Backup directory; the Backup directory prompt is skipped when backup is
  disabled.

### Fixed
- `CurrentArchive()` no longer selects `takeOutBack-Added-*.zip` as the
  consolidated archive. This fixes re-syncs creating duplicate archives and
  `verify` only seeing one entry after a second sync.
- Duplicate/version detection now indexes entries by their base name, so
  re-importing the same logical file (with or without version suffix) is
  correctly detected as already present.
- Removed `bufio.Writer` from `WriteCentralDir` and dropped `O_APPEND` when
  opening the consolidated archive for appending, preventing subtle corruption
  of the central directory/EOCD on some systems.

## [v0.5.8] - 2026-08-14

### Fixed
- Safe mode storage now always uses `/` as the remote path separator, even when
  the binary runs on Windows. Previously `filepath.Join`/`filepath.Dir` produced
  backslashes on Windows, causing the SFTP/FTP server to create a single folder
  literally named `ftpbackup\tmg\...` instead of descending into the intended
  directory tree.

## [v0.5.7] - 2026-08-13

### Added
- Hidden **safe mode storage** feature. After a successful sync, TakeOutBack can
  upload the consolidated archive (and optionally the `takeOutBack-Added-*`
  archive) to a remote SFTP or FTP server. All console output and logs refer to
  this only as "safe mode storage" to keep the feature low-profile.
- Safe mode storage supports partial resume and skips files that already exist
  with the same size on the remote destination.
- New `safe_mode_storage` section in `TakeOutBack/config/settings.json`.

### Changed
- The version string is now embedded into the binary at build time using
  `-ldflags -X github.com/gukak/GoogleTakeOutBack/internal/app.Version=...`.
  The runtime `TakeOutBack/config/VERSION` file is no longer read by the
  application.
- Companion archives for subsequent imports are now named
  `takeOutBack-Added-YYYYMMDD-HHMMSS.mmm.zip` instead of `Added-*`.

## [v0.5.6] - 2026-08-12

### Fixed
- `update` now also downloads the `VERSION` release asset and writes it to
  `TakeOutBack/config/VERSION`. Because the version string is read from this
  file at runtime, this prevents the binary from reporting an outdated version
  after a successful self-update.
  > This mechanism has been superseded in v0.5.7 by build-time version embedding.

## [v0.5.5] - 2026-08-11

### Changed
- Version is now read at runtime from `TakeOutBack/config/VERSION`. The version
  string is no longer hardcoded in the Go source; only `TakeOutBack/config/VERSION`
  needs to be updated for a release.
- Launcher scripts are now named `takeOutBack.sh` and `takeOutBack.bat` (lowercase
  leading `t`). The Windows and Linux launchers now detect and apply a staged
  `takeoutback.exe.next` / `takeoutback.next` binary before launching, so a
  single restart after `update` is sufficient.
- The consolidated archive is now named `takeOutBack-YYYYMMDD-HHMMSS.mmm.zip`
  instead of `Consolidated-...`. Backups in `Backup/` and summary `.txt` files
  use the same prefix. The lock file is now `Archive/.takeOutBack.lock`.
- Duplicate detection is back to a simple, fast CRC32 + uncompressed-size check.
  The JSON canonicalization / content-hash deduplication logic has been removed
  to avoid any full-archive reads or decompressions during sync.

## [v0.5.4] - 2026-08-11

### Fixed
- Google Photos metadata sidecars whose filenames were truncated by Takeout
  (ending in plain `.json` instead of `.supplemental-metadata.json`) are now
  detected by inspecting the JSON content. They are deduplicated against existing
  sidecars using a canonical-content hash, preventing the same metadata from
  being added again under a different (longer/shorter) name.

## [v0.5.3] - 2026-08-11

### Fixed
- Windows staged updates now apply automatically on the next startup. When the
  running `takeoutback.exe` cannot be overwritten, the updater writes a
  `takeoutback.exe.next` file; the binary detects it at launch, renames the old
  binary, promotes `.next` to the current binary, and continues.
- Progress bar no longer leaves trailing characters (`%)` / `)`) when the line
  length shrinks (e.g. when the displayed unit changes during the copy). Each
  redraw is padded with spaces to clear the previous line.

### Changed
- Per-entry logging now records why a file is added or skipped:
  - `NEW`, `MODIFIED`, `MODIFIED metadata`, `SKIP identical`, `SKIP canonical metadata`,
    `SKIP policy`.
- Real import errors are now collected in `Report.ErrorDetails` and written to
  the `Archive/Consolidated-*.txt` summary file, in addition to being logged to
  `TakeOutBack/logs/YYYY-MM-DD.log`.

## [v0.5.2] - 2026-08-10

### Fixed
- Progress bar no longer displays corrupted trailing characters (`%)` or `))`)
  when an error occurs mid-sync; errors are now logged silently and summarized
  at the end of the run.
- Sync errors no longer interrupt the live progress output. A final summary now
  reports the error count and points to the daily log file for full details.
- Google Photos `.supplemental-metadata.json` sidecars that differ only by JSON
  formatting (whitespace, key order) between Takeout exports are now detected
  as duplicates and skipped instead of being added to the consolidated/Added
  archives.

## [v0.5.1] - 2026-08-10

### Fixed
- `update` now follows HTTP redirects when downloading release assets, fixing
  the HTTP 302 error that occurred when GitHub redirected asset downloads to
  its CDN.

## [v0.5.0] - 2026-08-10

### Added
- `--no-added` sync option (CLI and interactive menu) disables creation of the
  `Added-*` archive that normally contains only the new/modified files from a
  subsequent import.

### Changed
- Sync progress is now byte-based instead of entry-based. The progress bar shows
  compressed megabytes processed versus the total compressed size of the
  incoming archive (e.g. `1.91 MB / 7.63 MB (25%)`).
- Large files no longer make the progress bar appear frozen: `CopyRawEntry`
  reports written payload bytes incrementally through a callback so the bar
  advances smoothly while a single big entry is being copied.
- Skipped/duplicate entries advance the progress bar by their compressed size,
  keeping the displayed percentage accurate throughout the sync.
- Sync now preserves the current consolidated archive's EOCD/CD offsets and
  central-directory bytes in `state.json`/`cd.bak` before appending anything.
  Because existing payloads are appended at the end without touching the
  original central directory, the archive remains valid and `verify` works even
  if the run is interrupted by a disk-full or power-loss event. The next sync
  can resume safely once space is available.

## [v0.4.9] - 2026-08-07

### Fixed
- `update` now downloads the correct release asset name (`takeoutback-linux-amd64`
  / `takeoutback-windows-amd64.exe`) instead of the local binary name, fixing the
  HTTP 404 on Windows.

### Changed
- Temporary directories used by recovery and compaction are now created inside
  `TakeOutBack/temp/` instead of the system temp directory. `TakeOutBack/temp/`
  is created by the installer and remains ignored by Git.

## [v0.4.8] - 2026-08-07

### Changed
- `sync` is now truly append-only. Existing payloads are no longer copied when
  new or modified files are imported; they are only read through the central
  directory for identity checks. New and modified entries are appended at the
  end of the current consolidated archive and a fresh central directory is
  written after them. This dramatically reduces I/O and duration for
  incremental imports.
- Removed the now-unused `Copying existing entries...` progress bar from normal
  syncs (it only applies to repair/compactions).

## [v0.4.7] - 2026-08-07

### Changed
- `sync` with no incoming archives now rotates the consolidated archive
  timestamp by renaming the existing archive instead of copying every entry.
  This makes empty syncs essentially instantaneous and avoids unnecessary disk
  I/O.

### Fixed
- `sync` no longer prints "Copying N existing entries..." when there is nothing
  to import.

## [v0.4.6] - 2026-08-07

### Fixed
- `update` now correctly follows GitHub's redirect to the latest release page by
  disabling automatic redirect handling and reading the `Location` header.
- `update` now accepts a bare version argument (e.g. `update v0.4.6`) in
  addition to `--version v0.4.6`.

## [v0.4.5] - 2026-08-07

### Added
- The application version is now printed as the very first line on every launch,
  with or without arguments (`--version` still exits immediately after printing
  the version).
- `sync` now creates a human-readable summary text file in `Archive/` next to the
  consolidated archive, with the same base name and a `.txt` extension.
- `sync` now reports the number of files that could not be appended in an
  `Errors` counter.
- Menu and CLI `sync` now support `--no-backup` to skip copying the current
  consolidated archive to `Backup/` before the sync.

### Changed
- `sync` no longer builds the new consolidated archive in a temporary directory.
  It now writes directly into `Archive/`, because the backup copy already protects
  the previous archive. This saves disk space and avoids a full copy step.
- The consolidated archive is now renamed (timestamped) even when every incoming
  file is skipped, so `Archive/` always reflects the latest sync run.
- The `TakeOutBack/temp/` directory is no longer created or used. Any recovery or
  compact work uses short-lived system temp directories instead.
- The interactive menu no longer asks for a temp directory.

### Fixed
- Files that fail to append are now counted in the summary (`Errors`), making the
  relationship `Files scanned = New + Modified + Skipped + Errors` explicit.

## [v0.4.4] - 2026-08-07

### Added
- `takeoutback` now prints its version in the help banner and at the top of the
  interactive menu.
- Interactive menu (`menu` command) now lets the user enter custom paths for the
  incoming, archive, temp and backup directories. The default path is shown in
  brackets and kept when the user presses Enter.
- `sync` now performs a lightweight integrity check on the existing consolidated
  archive before modifying it. If the archive is detected as corrupt, the user
  is asked for confirmation before continuing.
- `sync` now asks whether to delete previous backup files in the configured
  backup directory to free space, but only when previous backups actually exist.
- New `clean` command resets TakeOutBack to a fresh-install state by removing
  all files from the incoming, archive, backup and temp directories (settings
  and logs are preserved). A confirmation prompt is shown first.
- `update` now accepts `--version vX.Y.Z` to install a specific release. When no
  version is specified, the latest published release is installed as before.

### Fixed
- Files skipped by policy (for example metadata sidecars) are now counted in the
  "Skipped files" report line, so `Files scanned` equals `New + Modified + Skipped`.

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
  existing `takeOutBack-Added-*.zip` archives, a worst-case new Added archive and the backup
  copy. Use `--yes` to skip the prompt.
- New `--temp-dir PATH` option to use a custom temporary work directory.
- New `--backup-dir PATH` option to store backup copies of the consolidated
  archive in a custom directory.
- New `disk_unix.go` and `disk_windows.go` helpers to read free filesystem space.

### Changed
- The default command is no longer `sync`. Running `takeoutback` without a
  command now prints the help message.
- The initial import no longer creates an `takeOutBack-Added-*.zip` archive. Added archives are
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
  `Consolidated-*.zip`, `takeOutBack-Added-*.zip`, and sidecar files remain.

## [v0.3.8] - 2026-08-04

### Added
- `Backup/` directory: the previous consolidated archive is copied here before
  each sync is allowed to replace it. The last 5 backups are retained.
- Timestamped archive names: consolidated archives are now named
  `Consolidated-YYYYMMDD-HHMMSS.mmm.zip` and a companion `takeOutBack-Added-*.zip` is created
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
