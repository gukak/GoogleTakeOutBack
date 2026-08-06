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
- **Added-only archive**: each subsequent sync produces a companion `Added-*.zip`
  containing only the files imported during that run. The initial import does not
  create an Added archive.
- **Disk-space guard**: before writing anything, `sync` estimates the peak space
  required on each affected disk and asks for confirmation. Use `--yes` to run
  unattended.
- **Stale-lock recovery**: if a previous run was killed (Ctrl+C, power loss),
  the next run detects the abandoned lock and resumes safely.

## Quick Start

### Install on Linux

Run the following command in an **empty** directory:

```bash
curl -fsSL https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.4.1/install.sh | bash
```

### Install on Windows

Run the following command in an **empty** directory in PowerShell:

```powershell
irm https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.4.1/install.ps1 | iex
```

> See [Installation.md](docs/Installation.md) for more options (force install,
> specific version, fetch only the current platform binary).

### Use

1. Place your Google Takeout ZIP files in the `Incoming/` folder.
2. Run the backup command:
   - Linux: `./TakeOutBack.sh sync`
   - Windows: `TakeOutBack.bat sync`
3. TakeOutBack prints a plan with the space required on each affected disk and
   asks for confirmation. Add `--yes` to run without prompting.
4. Watch the progress: each archive is listed, then a progress bar shows the
   files being processed in real time.
5. Repeat whenever you have a new Takeout export.

Your consolidated archive lives at `Archive/Consolidated-YYYYMMDD-HHMMSS.mmm.zip`
(the local timestamp is updated on every sync). The first import creates only the
consolidated archive. Every later sync also produces a companion
`Added-YYYYMMDD-HHMMSS.mmm.zip` with only the files imported during that run, and
copies the previous consolidated archive to `Backup/` before replacing it.

All temporary work happens inside `TakeOutBack/temp/run-YYYYMMDD-HHMMSS.mmm/` by
default. You can override the archive, temp and backup directories from the
command line:

```bash
./TakeOutBack.sh sync --archive-dir /path/to/archive/disk --temp-dir /path/to/fast/ssd --backup-dir /path/to/backup/disk
```

You can also sync from a different source folder:

```bash
./TakeOutBack.sh sync --incoming /path/to/other/zips
```

If the disk-space estimate reports insufficient space but you still want to run
the backup, add `--force`:

```bash
./TakeOutBack.sh sync --force
```

### Live progress

During a sync you will see the list of archives and a per-archive progress bar:

```
Archives to process: 2
  1. takeout-2025-001.zip
  2. takeout-2025-002.zip
  [==============================] takeout-2025-001.zip 1542/1542 (100%)
  [===========>                  ] takeout-2025-002.zip  623/1823 (34%)
```

When `sync` is invoked, TakeOutBack first shows a plan such as:

```
Sync plan:
  Incoming archives: 2, total size: 4.52 GiB
  Existing consolidated archive: 12.34 GiB
  Existing added archives: 0 B
  Estimated peak space required by disk:
    /path/to/Archive: required 29.70 GiB, free 45.21 GiB [OK]
  Status: OK
Proceed with backup? [y/N]
```

When the run finishes, the usual summary is printed, including the paths to the
new consolidated archive and, for subsequent imports, the added-only archive:

```
TakeOutBack v0.4.1
Archives scanned : 2
Files scanned    : 3365
New files        : 142
Modified files   : 8
Skipped files    : 3215
Bytes appended   : 1.42 GiB
Duration         : 00:02:14
Status           : OK
Archive: /path/to/Archive/Consolidated-20260805-123045.123.zip
Added:   /path/to/Archive/Added-20260805-123045.123.zip
```

## Commands

| Command | Description |
| --- | --- |
| `sync` | Plan and run the backup consolidation (requires confirmation) |
| `sync --yes` | Run the backup without confirmation |
| `sync --force` | Run the backup even if the space estimate is insufficient |
| `verify` | Check archive integrity |
| `verify --deep` | Re-decompress every entry and verify CRC32 |
| `stats` | Show archive statistics |
| `compact` | Rewrite archive to remove dead central-directory blocks |
| `update` | Update the binary from GitHub Releases |
| `menu` | Interactive menu |
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
