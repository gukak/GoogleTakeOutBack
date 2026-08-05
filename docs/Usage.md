# Usage Guide

## Daily Workflow

1. Download your latest Google Takeout export as a ZIP file.
2. Copy or move it into the `Incoming/` folder inside your TakeOutBack project.
3. Run the launcher:
   - Linux: `./TakeOutBack.sh`
   - Windows: `TakeOutBack.bat`
4. Watch the live progress: TakeOutBack lists the archives it found, then shows
   a progress bar for each archive while it is being processed.
5. Read the final summary printed to the console.

If you want to import from a different folder than `Incoming/`, use the
`--incoming` option:

```bash
./TakeOutBack.sh --incoming /path/to/other/zips
./TakeOutBack.sh sync --incoming /path/to/other/zips
./TakeOutBack.sh --incoming=/path/to/other/zips
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
  [===============================>] takeout-2025-001-of-001.zip 1542/1542 (100%)
  [===========>                  ] takeout-2025-001-of-002.zip  623/1823 (34%)
```

The progress bar is rendered with carriage returns (`\r`) so it updates in
place in any terminal (Windows Command Prompt, PowerShell, Linux console). If
stdout is redirected to a file or pipe, each update appears on its own line,
which is still readable.

## Understanding the Summary

After each sync you will see something like:

```
TakeOutBack v0.3.7
Archives scanned : 4
Files scanned    : 182345
New files        : 523
Modified files   : 12
Skipped files    : 181810
Bytes appended   : 1.42 GiB
Duration         : 00:02:14
Status           : OK
```

- **Archives scanned**: number of ZIP files found in `Incoming/`.
- **Files scanned**: total entries found in those archives.
- **New files**: entries that were not yet in the consolidated archive.
- **Modified files**: entries whose path was already archived but whose CRC or
  size changed. They are stored as a new version (`name__v2.ext`, `name__v3.ext`).
- **Skipped files**: entries that were already present with identical CRC and size.

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

This is the default action when you run `./TakeOutBack.sh` without arguments.

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

Checks GitHub Releases for a newer version and downloads it. Your archives and
configuration are never touched.

### Interactive Menu

```bash
./TakeOutBack.sh menu
```

Shows a numbered menu for users who prefer not to type commands.

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
