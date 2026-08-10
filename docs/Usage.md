# Usage Guide

## Daily Workflow

1. Download your latest Google Takeout export as a ZIP file.
2. Copy or move it into the `Incoming/` folder inside your TakeOutBack project.
3. Run the backup command:
   - Linux: `./TakeOutBack.sh sync`
   - Windows: `TakeOutBack.bat sync`
4. Watch the live progress: TakeOutBack lists the archives it found, then shows
   a byte-based progress bar for each archive while it is being processed (for
   example `12.4 MB / 156.8 MB (8%)`). The bar advances smoothly even when a
   single file is very large, because payload bytes are reported as they are
   copied. On subsequent imports, progress bars are also shown for backing up
   the current consolidated archive.
5. Read the final summary printed to the console.

TakeOutBack writes the following files:

- `Archive/Consolidated-YYYYMMDD-HHMMSS.mmm.zip` — the current consolidated archive
  (timestamps are in the system's local time). The consolidated archive always
  gets a new timestamped name, even when every incoming file is skipped.
- `Archive/Added-YYYYMMDD-HHMMSS.mmm.zip` — only the files added during this run.
  This file is created only for subsequent imports that contain new or modified
  files; the first import produces only the consolidated archive.
- `Archive/Consolidated-YYYYMMDD-HHMMSS.mmm.txt` — a human-readable summary of the
  run, identical to the console output.
- `Backup/Consolidated-YYYYMMDD-HHMMSS.mmm.zip` — a copy of the previous consolidated
  archive, kept before it is replaced. The last 5 backups are retained.

You can override the archive and backup directories from the command line:

```bash
./TakeOutBack.sh sync --archive-dir /path/to/archive/disk --backup-dir /path/to/backup/disk
```

To skip the backup copy:

```bash
./TakeOutBack.sh sync --no-backup
```

To skip the companion `Added-*` archive (only the consolidated archive is
produced):

```bash
./TakeOutBack.sh sync --no-added
```

If a sync is interrupted (Ctrl+C, power loss, drive removal, full disk), the
next run will automatically detect the stale lock and resume. Your previous
consolidated archive is untouched because TakeOutBack never modifies it in
place: it records the current central-directory offsets in `state.json` and
backs up the central directory in `cd.bak` before appending new payloads. New
files are added after the existing end-of-central-directory record, so the
archive remains valid even if the run stops mid-write. A fresh sync can be
started as soon as space is available.

If you want to import from a different folder than `Incoming/`, use the
`--incoming` option:

```bash
./TakeOutBack.sh sync --incoming /path/to/other/zips
./TakeOutBack.sh sync --incoming=/path/to/other/zips
```

6. Optionally delete the original Takeout ZIP from `Incoming/` (or the custom
   folder) if you want to free space. TakeOutBack never deletes incoming files
   automatically.

## Live Progress

While a sync is running, TakeOutBack prints the list of archives it will
process, then redraws a progress bar for each archive on the same terminal line:

```
Archives to process: 4
  1. takeout-2025-001-of-001.zip
  2. takeout-2025-001-of-002.zip
  3. takeout-2025-001-of-003.zip
  4. takeout-2025-001-of-004.zip
  [===============================>] takeout-2025-001-of-001.zip 45.2 MB / 45.2 MB (100%)
  [===========>                  ] takeout-2025-001-of-002.zip 18.6 MB / 52.4 MB (35%)
```

The progress bar is rendered with carriage returns (`\r`) so it updates in
place in any terminal (Windows Command Prompt, PowerShell, Linux console). It
shows compressed megabytes processed versus the total compressed size of the
incoming archive, so the percentage stays accurate even when many files are
skipped or when one file dominates the archive. If stdout is redirected to a
file or pipe, each update appears on its own line, which is still readable.

## Understanding the Summary

After each sync you will see something like:

```
TakeOutBack v0.4.9
Archives scanned : 4
Files scanned    : 182345
New files        : 523
Modified files   : 12
Skipped files    : 181810
Errors           : 0
Bytes appended   : 1.42 GiB
Duration         : 00:02:14
Status           : OK
Archive: /path/to/Archive/Consolidated-20260805-123045.123.zip
Added:   /path/to/Archive/Added-20260805-123045.123.zip
```

- **Archives scanned**: number of ZIP files found in `Incoming/`.
- **Files scanned**: total entries found in those archives.
- **New files**: entries that were not yet in the consolidated archive.
- **Modified files**: entries whose path was already archived but whose CRC or
  size changed. They are stored as a new version (`name__v2.ext`, `name__v3.ext`).
- **Skipped files**: entries that were already present with identical CRC and
  size. Google Photos `.supplemental-metadata.json` sidecars are also skipped
  when they are semantically identical, even if their raw bytes differ (for
  example different whitespace or key ordering).
- **Errors**: number of entries that could not be processed. Details are written
  to `TakeOutBack/logs/YYYY-MM-DD.log` and the summary points to that file when
  errors occur.
- **Archive / Added**: paths to the new consolidated archive and, for subsequent
  imports, the archive containing only the files added in this run.

## File Versioning

The first time a file is seen, it is stored under its original name.

```
Photos/image.jpg
```

When a later Takeout contains a different version of the same file, the new
version is stored as:

```
Photos/image__v2.jpg
```

Subsequent versions become `image__v3.jpg`, `image__v4.jpg`, etc. The version
suffix is inserted immediately before the final extension. Files without an
extension receive the suffix at the end.

## Deleted Files

If a file existed in an earlier Takeout but is missing from the latest one, it
is **not** removed from the consolidated archive. The history is preserved
forever.

## Commands

### Synchronize

```bash
./TakeOutBack.sh sync
```

Running `./TakeOutBack.sh` without arguments now shows the help and does **not**
start a backup automatically. You must explicitly use the `sync` command.

To skip the backup of the current consolidated archive:

```bash
./TakeOutBack.sh sync --no-backup
```

### Verify

```bash
./TakeOutBack.sh verify
```

Performs a metadata check. Use `--deep` to re-decompress every entry and verify
CRC32:

```bash
./TakeOutBack.sh verify --deep
```

### Statistics

```bash
./TakeOutBack.sh stats
```

Shows archive size, number of entries, unique paths and compression ratio.

### Compact

```bash
./TakeOutBack.sh compact
```

Rewrites the archive to remove dead central-directory blocks that accumulate
over many append-only syncs. This is safe and never loses data; it is only
needed when the archive becomes noticeably larger than its payload content.

### Update

```bash
./TakeOutBack.sh update
```

Checks GitHub Releases for a newer version and downloads it. Your archives,
backups and incoming files are never touched.

To install a specific release instead of the latest one:

```bash
./TakeOutBack.sh update --version v0.4.9
# or equivalently:
./TakeOutBack.sh update v0.4.9
```

### Clean / Reset

```bash
./TakeOutBack.sh clean
```

Removes all files from the incoming, archive and backup directories after
asking for confirmation. This resets TakeOutBack to a fresh-install state while
preserving settings and logs.

### Interactive Menu

```bash
./TakeOutBack.sh menu
```

Shows a numbered menu for users who prefer not to type commands. The menu lets
you enter custom paths for the incoming, archive and backup directories; the
default path is shown in brackets and kept if you press Enter. Before syncing,
the menu also asks whether to create a backup of the current consolidated archive.

## Logs

Each sync writes a log entry to `TakeOutBack/logs/YYYY-MM-DD.log`. Logs are
JSON-formatted and contain the date, level and message. Keep only the last 90
days by default (configurable in `TakeOutBack/config/settings.json`).

## Configuration

Edit `TakeOutBack/config/settings.json` to change runtime behavior.

```json
{
  "log_level": "info",
  "log_retention_days": 90,
  "fetch_both_platforms": true,
  "keep_metadata_sidecars": true,
  "drop_incoming_after_sync": false,
  "re_deflate_store": false,
  "auto_compact_threshold_mb": 0
}
```

- `fetch_both_platforms`: download both Linux and Windows binaries during install
  so the same folder works on either OS.
- `keep_metadata_sidecars`: keep Google Takeout's own HTML/JSON metadata files
  inside the archive.
- `drop_incoming_after_sync`: **not implemented**; reserved for future policy.
- `re_deflate_store`: **not implemented**; reserved for future policy.
- `auto_compact_threshold_mb`: **not implemented**; reserved for future policy.

## Moving to Another Drive or Computer

Copy the entire project folder. Because every dependency is inside the folder,
TakeOutBack remains fully functional. Run the appropriate launcher on the target
OS.

If you move from a Windows filesystem (NTFS/exFAT) to Linux, the launcher script
automatically restores the executable bit on the Linux binary.
