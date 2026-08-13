# TakeOutBack

A portable, offline, cross-platform application that consolidates multiple
Google Takeout ZIP exports into a single logical archive history.

**No database. No server. No installation. No admin rights.**

---

## Features

- **Truly portable**: one folder tree; copy it to any disk or computer and run it.
- **Cross-platform**: identical behavior on Windows 10/11 x64 and Linux x86_64.
- **ZIP only**: input and output are standard ZIP files; no other archive format.
- **Never loses history**: keeps every file, every version of modified files and
  every file later deleted from Google.
- **Crash-safe**: each sync writes new timestamped archives from scratch; the
  previous consolidated archive is backed up in `Backup/` before being replaced.
- **Self-contained**: all required binaries are shipped inside the project tree;
  no dependency on system `zip`, `unzip`, Python or other tools.
- **Self-updating**: built-in updater fetches new binaries from GitHub Releases
  without touching your archives.
- **Added-only archive**: each subsequent sync produces a companion `takeOutBack-Added-*.zip` containing
  only the files imported during that run.
- **Stale-lock recovery**: if a previous run was killed (Ctrl+C, power loss),
  the next run detects the abandoned lock and resumes safely.

## Quick Start

### Install on Linux

Run the following command in an **empty** directory:

```bash
curl -fsSL https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.4.9/install.sh | bash
```

### Install on Windows

Run the following command in an **empty** directory in PowerShell:

```powershell
irm https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.4.9/install.ps1 | iex
```

> See [Installation.md](docs/Installation.md) for more options (force install,
> specific version, fetch only the current platform binary).

### Use

1. Place your Google Takeout ZIP files in the `Incoming/` folder.
2. Run the backup command:
   - Linux: `./takeOutBack.sh sync`
   - Windows: `takeOutBack.bat sync`
3. Watch the progress: each archive is listed, then a byte-based progress bar
   shows the compressed megabytes being processed in real time. The bar advances
   smoothly even for a single very large file, and skipped/duplicate entries
   still advance the count. On subsequent imports you will also see a progress
   bar for the backup of the current archive.
4. Repeat whenever you have a new Takeout export.

Your consolidated archive lives at `Archive/takeOutBack-YYYYMMDD-HHMMSS.mmm.zip`
(the local timestamp is updated on every sync, even when nothing changes). The
first import creates only the consolidated archive. Every later sync that
contains new or modified files also produces a companion
`Added-YYYYMMDD-HHMMSS.mmm.zip` with only the files imported during that run, and
copies the previous consolidated archive to `Backup/` before replacing it.

The current consolidated archive is never modified in place. Before appending
new files, TakeOutBack records the current central-directory offsets in
`state.json` and backs up the central directory in `cd.bak`. New payloads are
appended after the existing end-of-central-directory record, and a fresh central
directory is written after them. If a run is interrupted by a power loss, drive
removal, or a full disk, the existing archive stays valid and `verify archive`
continues to work; a fresh sync can be started as soon as space is available.

### Duplicate detection

Files are identified by their normalized path, CRC32 and uncompressed size. In
addition, Google Photos `.supplemental-metadata.json` sidecars are compared
semantically: two metadata files that contain the same data but were serialized
with different whitespace or key ordering are treated as the same file and are
not added again.

You can override the archive and backup directories from the command line:

```bash
./takeOutBack.sh sync --archive-dir /path/to/archive/disk --backup-dir /path/to/backup/disk
```

To skip the backup copy (for example when disk space is very tight):

```bash
./takeOutBack.sh sync --no-backup
```

You can also sync from a different source folder:

```bash
./takeOutBack.sh sync --incoming /path/to/other/zips
```

To skip the `Added-*` archive that normally contains only the new or modified
files from a subsequent import:

```bash
./takeOutBack.sh sync --no-added
```

### Live progress

During a sync you will see the list of archives and a per-archive progress bar:

```
Archives to process: 2
  1. takeout-2025-001.zip
  2. takeout-2025-002.zip
  [==============================] takeout-2025-001.zip 45.2 MB / 45.2 MB (100%)
  [===========>                  ] takeout-2025-002.zip 18.6 MB / 52.4 MB (35%)
```

When the run finishes, the usual summary is printed, including the paths to the
new consolidated archive and, for subsequent imports, the takeOutBack-Added archive:

```
TakeOutBack v0.4.9
Archives scanned : 2
Files scanned    : 3365
New files        : 142
Modified files   : 8
Skipped files    : 3215
Errors           : 0
Bytes appended   : 1.42 GiB
Duration         : 00:02:14
Status           : OK
Archive: /path/to/Archive/takeOutBack-20260805-123045.123.zip
Added:   /path/to/Archive/Added-20260805-123045.123.zip
```

TakeOutBack also writes a summary text file next to the consolidated archive,
for example `Archive/takeOutBack-20260805-123045.123.txt`.

## Commands

| Command | Description |
| --- | --- |
| `sync` | Consolidate new and changed files from `Incoming/` |
| `sync --no-backup` | Same, without copying the current archive to `Backup/` |
| `verify` | Check archive integrity |
| `verify --deep` | Re-decompress every entry and verify CRC32 |
| `stats` | Show archive statistics |
| `compact` | Rewrite archive to remove dead central-directory blocks |
| `update` | Update the binary from GitHub Releases |
| `update --version vX.Y.Z` | Install a specific release |
| `clean` | Reset incoming, archive and backup files (asks confirmation) |
| `menu` | Interactive menu |
| `--version` | Print the current version |
| `--help` | Show available commands and options |

## How It Works

- File identity is determined by `(path, uncompressed size, CRC32)` taken from
  the ZIP metadata. No SHA-256 hashing is required for normal operation.
- If a file is unchanged, it is skipped.
- If a file is modified, the new version is stored as `name__v2.ext`,
  `name__v3.ext`, etc. The original stays at `name.ext`.
- If a file disappears from future Takeouts, the historical copies are kept
  forever.
- The archive is updated append-only: new payloads are appended at the end and
  a new central directory is written. Existing data is never overwritten.

## Documentation

- [Architecture](docs/Architecture.md) — design, algorithms and trade-offs
- [Installation](docs/Installation.md) — detailed install and upgrade instructions
- [Usage](docs/Usage.md) — day-to-day operation
- [Development](docs/Development.md) — building, testing and releasing
- [Troubleshooting](docs/Troubleshooting.md) — common problems and recovery

## License

MIT
