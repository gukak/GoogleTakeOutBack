# Troubleshooting Guide

## General Approach

TakeOutBack is intentionally simple. Almost every problem can be diagnosed by:

1. Reading the latest log file in `TakeOutBack/logs/`.
2. Running `./takeOutBack.sh verify`.
3. Checking that the project layout is intact.

## Common Issues

### "takeoutback: binary not found"

The launcher cannot find the native binary. Ensure:

- `TakeOutBack/tools/linux/takeoutback` exists on Linux.
- `TakeOutBack\tools\windows\takeoutback.exe` exists on Windows.
- You are running the launcher from the project root.

If you copied the folder from a Windows/exFAT drive to Linux, the Linux binary
may have lost its executable bit. The launcher tries to restore it automatically;
if that fails, run:

```bash
chmod +x TakeOutBack/tools/linux/takeoutback
```

### "cannot acquire lock"

Another TakeOutBack instance is already running. If you are sure no other
instance is running, delete the stale lock file:

```bash
rm Archive/.takeOutBack.lock
```

### Sync reports modified files every time

This can happen if the same Takeout ZIP is processed repeatedly and the archive
does not yet contain a matching version. Run sync once, then run it again. The
second run should report all files as skipped. If it does not, run:

```bash
./takeOutBack.sh verify --deep
```

### Archive appears corrupted after a crash

TakeOutBack runs recovery automatically at the start of every sync. If recovery
fails, the log file will contain details. You can force a full rebuild with:

```bash
./takeOutBack.sh compact
```

`compact` reads every valid local file header and writes a brand-new archive.
No historical data is lost.

### Verify reports CRC errors

A CRC error means a payload does not match its recorded checksum. This can be
caused by:

- A bad download of an incoming Takeout ZIP.
- Bit rot on the storage device.
- A bug in an older version of TakeOutBack.

First, re-download the suspect Takeout ZIP and run sync again. If the error
persists, run:

```bash
./takeOutBack.sh compact
./takeOutBack.sh verify --deep
```

### Installer fails to download files

The installer downloads assets from GitHub Releases. If the repository URL is
still the placeholder `gukak/GoogleTakeOutBack`, downloads will fail. Replace it
with your real owner/repository before releasing.

If you are offline, the installer cannot work. Use the manual install procedure
described in [Installation.md](Installation.md).

### Update says "Already up to date" but a newer release exists

The updater compares the embedded version string in the running binary with the
latest GitHub Release tag. Ensure:

- The binary was built with the correct `-ldflags` version (or use the official
  release asset).
- The newer release is actually published (not a draft).
- You have network connectivity.

### Windows: "cannot create directory F:\\"Incoming"

This was caused by the batch launcher passing `--root "F:\"` where the trailing
backslash escaped the closing quote. It is fixed in v0.3.6 and later. Reinstall
with the latest installer:

```powershell
irm https://github.com/OWNER/REPO/releases/download/v0.4.9/install.ps1 | iex
```

### Stale lock after a crash or Ctrl+C

If a sync is interrupted, the lock file `Archive/.takeOutBack.lock` may be left
behind. TakeOutBack detects this automatically on the next run: it reads the
PID stored in the lock, and if that process is no longer alive it removes the lock
and continues. You only need to delete the lock manually if the PID detection
fails on your platform:

```bash
rm Archive/.takeOutBack.lock
```

### Interrupted sync left partial files

Since v0.4.9, TakeOutBack writes the new consolidated archive directly into
`Archive/`. If a sync is interrupted, the latest `takeOutBack-*.zip` may be
partial. The previous archive is safely stored in `Backup/`, so you can restore
from there if needed.

If you manually find a `*.tmp`, `*.rebuild` or `*.compact` file in `Archive/`,
it is safe to delete. The current consolidated archive is always named
`takeOutBack-YYYYMMDD-HHMMSS.mmm.zip` and never has a `.tmp` suffix.

### Progress bar appears on many lines instead of one

The progress bar uses carriage returns (`\r`) to redraw in place. This is the
expected behavior when stdout is captured, redirected to a file or viewed in a
non-terminal environment. In a normal terminal window the bar updates on a
single line.

### Windows Defender or antivirus warns about the binary

Static Go binaries are sometimes flagged by heuristics. The binary is built from
the published source with `CGO_ENABLED=0`. You can rebuild it yourself to verify.

## Reporting Bugs

Include:

- The exact command you ran.
- The full output.
- The relevant log file from `TakeOutBack/logs/`.
- Your operating system version.
- The output of `./takeOutBack.sh --version`.

## Recovery Checklist

1. Stop any running TakeOutBack instance.
2. Back up `Archive/Consolidated.zip` if possible.
3. Run `./takeOutBack.sh verify --deep`.
4. If errors are found, run `./takeOutBack.sh compact`.
5. Run `./takeOutBack.sh verify --deep` again.
6. If problems persist, open an issue with the logs.
