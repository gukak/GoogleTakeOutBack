# Installation Guide

TakeOutBack is designed to be installed from an empty directory with a single
command. Everything it needs is downloaded into that directory; nothing is
installed on the host operating system.

## Before You Begin

You need a GitHub repository that hosts the release assets. The placeholder
owner/repository in the installer scripts is `gukak/GoogleTakeOutBack`. Before
your first release, replace this with your real repository in:

- `TakeOutBack/scripts/install.sh`
- `TakeOutBack/scripts/install.ps1`
- `src/internal/app/app.go` (the `OwnerRepo` constant)

## Linux / macOS

Open a terminal in the empty directory where you want the project, then run:

```bash
curl -fsSL https://github.com/OWNER/REPO/releases/download/v0.3.8/install.sh | bash
```

With options:

```bash
# Force install into a non-empty directory
curl -fsSL https://github.com/OWNER/REPO/releases/download/v0.3.8/install.sh | bash -s -- --force

# Install a specific version
TAKEOUTBACK_VERSION=v0.3.8 curl -fsSL https://github.com/OWNER/REPO/releases/download/v0.3.8/install.sh | bash

# Do not download the Windows binary
TAKEOUTBACK_FETCH_BOTH=0 curl -fsSL https://github.com/OWNER/REPO/releases/download/v0.3.8/install.sh | bash
```

The installer creates the project tree, downloads the Linux binary, optionally
the Windows binary, verifies SHA-256 checksums, and writes the launcher scripts.

## Windows

Open PowerShell in the empty directory where you want the project, then run:

```powershell
irm https://github.com/OWNER/REPO/releases/download/v0.3.8/install.ps1 | iex
```

With options:

```powershell
# Force install into a non-empty directory
irm https://github.com/OWNER/REPO/releases/download/v0.3.8/install.ps1 | iex -- -Force

# Install a specific version
$env:TAKEOUTBACK_VERSION = "v0.3.8"
irm https://github.com/OWNER/REPO/releases/download/v0.3.8/install.ps1 | iex

# Do not download the Linux binary
irm https://github.com/OWNER/REPO/releases/download/v0.3.8/install.ps1 | iex -- -NoBoth
```

## Manual Install

If you prefer not to pipe scripts from the internet:

1. Download `takeoutback-linux-amd64` (and optionally `takeoutback-windows-amd64.exe`)
   from the release page.
2. Verify the `.sha256` checksum files.
3. Create the directory structure shown in `README.md`.
4. Place the binaries in `TakeOutBack/tools/linux/` and `TakeOutBack/tools/windows/`.
5. Copy `TakeOutBack.sh`, `TakeOutBack.bat` and the files from `TakeOutBack/config/`
   to the project root.

## Update

To update the binary and launcher scripts without touching your archives:

```bash
./TakeOutBack.sh update
```

On Windows:

```powershell
TakeOutBack.bat update
```

The updater checks the latest GitHub Release, downloads the matching binary,
verifies its SHA-256 checksum and replaces the local binary atomically.

## Post-Install

1. Copy Google Takeout ZIP files into `Incoming/`.
2. Run `./TakeOutBack.sh` (Linux) or `TakeOutBack.bat` (Windows).
3. The consolidated archive is written to `Archive/Consolidated-YYYYMMDD-HHMMSS.mmm.zip`.
4. A companion `Added-*.zip` with only the new/modified files is written next to it.
5. The previous consolidated archive is copied to `Backup/` before being replaced.

No PATH, registry, scheduled task or service is created.
