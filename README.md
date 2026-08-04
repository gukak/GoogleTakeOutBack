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
- **Efficient**: reads ZIP central directories and copies raw compressed streams;
  never extracts full archives and never rewrites the archive on every sync.
- **Crash-safe**: append-only archive, atomic sidecar updates and automatic
  recovery from power loss or forced termination.
- **Self-contained**: all required binaries are shipped inside the project tree;
  no dependency on system `zip`, `unzip`, Python or other tools.
- **Self-updating**: built-in updater fetches new binaries from GitHub Releases
  without touching your archives.

## Quick Start

### Install on Linux

Run the following command in an **empty** directory:

```bash
curl -fsSL https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.3.0/install.sh | bash
```

### Install on Windows

Run the following command in an **empty** directory in PowerShell:

```powershell
irm https://github.com/gukak/GoogleTakeOutBack/releases/download/v0.3.0/install.ps1 | iex
```

> Replace `gukak/GoogleTakeOutBack` with your real GitHub owner/repository before
> the first release. See [Installation.md](docs/Installation.md) for details.

### Use

1. Place your Google Takeout ZIP files in the `Incoming/` folder.
2. Run the launcher:
   - Linux: `./TakeOutBack.sh`
   - Windows: `TakeOutBack.bat`
3. Repeat whenever you have a new Takeout export.

Your consolidated archive lives at `Archive/Consolidated.zip`.

## Commands

| Command | Description |
| --- | --- |
| `sync` (default) | Consolidate new and changed files from `Incoming/` |
| `verify` | Check archive integrity |
| `verify --deep` | Re-decompress every entry and verify CRC32 |
| `stats` | Show archive statistics |
| `compact` | Rewrite archive to remove dead central-directory blocks |
| `update` | Update the binary from GitHub Releases |
| `menu` | Interactive menu |

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
