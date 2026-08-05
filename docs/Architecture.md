# TakeOutBack — Architecture Document

> Status: **Implemented v0.3.7** — the design described here is implemented and
> released. This document is updated to reflect the current behavior.

---

## 1. Executive Summary

TakeOutBack is a portable, offline, cross-platform (Windows 10/11 x64, Linux x86_64)
application that consolidates multiple Google Takeout ZIP exports into a single
append-only ZIP archive (`Consolidated.zip`) while preserving every historical
file, every historical version of modified files, and all files later removed from
the Google account. No database, no server, no extraction of full archives.

The recommended architecture is:

- A **single self-contained native binary** (per platform) implementing the
  synchronization engine, written in **Go**.
- **ZIP as the only storage format** for both input and output.
- An **append-only consolidated archive** with a rewritten central directory at
  each sync, plus a tiny state sidecar (`Consolidated.zip.state.json`) and a
  backup central-directory sidecar (`Consolidated.zip.cd.bak`) for crash safety.
- **File identity by ZIP metadata**: `(normalized internal path, uncompressed
  size, CRC32)`, taken directly from the ZIP central directories. SHA-256 is
  used only for verifying downloaded binaries and integrity-checking the
  consolidated archive on demand — **never** for routine file identity.
- **History preserved by appending versioned names** (`Photos/image.jpg` stays
  the original; later versions become `Photos/image__v2.jpg`, `Photos/image__v3.jpg`).
- **Portable binaries hosted as GitHub Release assets** (not committed, not Git-LFS).
- A **single one-shot installer** per platform (`install.sh` / `install.ps1`),
  downloaded via `curl|bash` / `irm|iex`, that pulls a *pinned release tag* and
  verifies SHA-256 checksums before placing files.
- An **in-place self-update** that only swaps the binary and the runner scripts,
  never touches user archives.

The design favors **simplicity, robustness, portability, maintainability and
long-term reliability** as explicitly required.

---

## 2. Requirements Recap & Constraints

Hard constraints (non-negotiable):

| # | Constraint | Consequence |
|---|---|---|
| C1 | Runs on Windows 10/11 x64 and Linux x86_64, identical behavior | One codebase, cross-compiled per platform; no platform-specific data layout |
| C2 | Fully portable, no install, no admin, no env vars, no services | Single folder tree, runtime is the binary itself, no system integration |
| C3 | ZIP is the only allowed format (input and output) | All storage, history and identity live inside a ZIP file |
| C4 | No databases | State is deduced from ZIP metadata; optional tiny JSON sidecar |
| C5 | No massive extraction | Operate on ZIP central directories; copy raw deflate streams byte-for-byte |
| C6 | Environment survives power loss / forced termination / USB removal | Atomic writes, append-only archive, sidecar state, recovery on startup |
| C7 | All binaries embedded locally, never from system PATH | `tools/<platform>/` holds the binary; launcher invokes it by absolute path |
| C8 | Installable from an empty folder with one command | `curl|bash` and `irm|iex` install scripts |
| C9 | Cross-platform archive interchange (Linux-made usable on Windows and vice versa) | Forward-slash paths, UTC DOS timestamps, no host-specific bytes |
| C10 | Single-maintainer sustainability over years | Small codebase, stdlib-only, no framework churn, documented algorithms |
| C11 | Never lose history (all files, all versions, deleted files kept forever) | Append-only archive, never overwrite, never delete entries |

---

## 3. Key Architectural Decisions (and Why)

### 3.1 Runtime / Language — **Go** (recommended)

**Decision**: Implement the engine as one static, self-contained **Go** binary per
platform. The binary is shipped inside the project tree (in `tools/<platform>/`)
and invoked by the launcher scripts. **No interpreter, no runtime, no libraries
are ever required on the host.**

**Why over alternatives**:

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **Go (static binary)** | Single static binary per OS (~10–15 MB), true cross-compile from one host, batteries-included `archive/zip` stdlib (CRC32, central directory read/write, raw deflate stream copy via `ioSectionReader`), no runtime to bundle, trivial offline pack, tiny attack surface, multi-year LTS, easy to learn for any single maintainer | Maintainer must be comfortable in Go (the brief lists Python as a core skill) | **Recommended** |
| **Python + embeddable distribution** | Matches the brief's listed Python expertise; readable code; rich ZIP library (`zipfile`); embeddable zip on Windows (~12 MB), redistributable CPython build on Linux | Must bundle an interpreter and runtime per OS (~30–50 MB per platform); depends on external packages for raw deflate copy / ZIP64 nuances; packaging Python apps as robust offline binaries is fragile historically (broken wheels, path issues, `sys.path` surprises in the embeddable zip which has no `pip` by default); larger long-term surface | **Viable but heavier**, technically near-equivalent, harder to keep rock-solid for years |
| **Rust (static binary)** | Properties similar to Go, even better safety guarantees | Steeper for a single maintainer; slower iteration; `zip` crate good but ecosystem churn risk | Viable, but **more complex than needed** |
| **Pure Bash/PowerShell + bundled CLI tools (zip/unzip/7z)** | Minimal "code" | Shell is too weak for metadata-only diff, CRC extraction, central-directory surgery without recompiling; cross-platform parity hard; brittle quoting/encoding rules | **Rejected** (does not satisfy C5 and C6 cleanly) |
| **C/C++** | Ultimate control | Developer productivity and maintainability loss; binary distribution headaches | **Rejected** (violates C10 for a single maintainer) |

**Notes on the Go choice**:

- Go's `archive/zip` provides everything needed:
  - `zip.OpenReader` to walk central directory of incoming Takeout ZIPs.
  - `zip.File.CRC32`, `zip.File.UncompressedSize64`, `zip.File.Method` exposed directly.
  - `zip.Writer` to construct the new central directory.
  - Raw-stream copy: `f.Open()` gives a section reader over the **compressed** bytes when `Method != Store`, allowing a byte-exact copy of an entry's deflate stream into the output archive without re-compressing. For `Store` entries we may optionally re-deflate or copy raw (documented in §7).
- Go binaries run on Windows x64, Linux x86_64 with no shared-object dependencies (`CGO_ENABLED=0`).
- Cross-platform file handles, fsync (`os.File.Sync()`), and os-specific safe rename (`os.Rename` is atomic on both Win10+ and POSIX) are in stdlib.
- Standard library only: zero third-party dependencies at runtime. This is the single biggest factor in **multi-year maintainability**.

> **Decision:** Go static binary. The portability, reliability and "no-runtime"
> constraints dominate. The implementation is a single stdlib-only Go binary per
> platform. The decision is recorded in §14.

### 3.2 Storage model — **single append-only ZIP archive**

**Decision**: The complete historical collection lives in exactly one ZIP file,
`Archive/Consolidated.zip`. It is **append-only**: file payloads are appended
at the end; the central directory is rewritten at the end at each sync; never
overwritten, never truncated except in the documented recovery/compaction paths.

Detailed mechanics in §7. Rationale here.

Why one archive (vs alternatives in §4):

- Satisfies C3 (ZIP only) and the "single logical consolidated archive" requirement literally.
- Append-only = monotonic growth = trivially crash-safe if we never overwrite known-good bytes.
- One file is easier to back up, copy, move, inspect than a tree of files.
- Cross-platform byte layout: ZIP bodies are platform-independent.

### 3.3 State model — **deduced from ZIP, with tiny JSON sidecar**

**Decision**: No database. The **consolidated archive's central directory is the
source of truth** for what files exist, their versions and their sizes/CRCs.

A tiny sidecar `Archive/Consolidated.zip.state.json` (a few hundred bytes —
**NOT user data, archive-derivable**) stores only:

- `version` (sidecar format version, e.g. `1`)
- `archive_path` (relative)
- `archive_end` (current size of `Consolidated.zip`)
- `entries` (count)
- `cd_offset` (offset of the **last valid** central directory)
- `cd_size` (bytes)
- `cd_sha256` (sha256 of the central-directory bytes — verifies integrity on load)
- `eocd_offset` (end-of-central-directory record offset)
- `last_sync_at`, `last_sync_duration_s`, `tool_version`

It can always be **reconstructed from the archive** (by scanning all local file
headers). It is a **cache + crash-recovery aid**, not a database. Justified by
C6 (crash recovery must be fast) and by avoiding repeated full scans on every
startup. This satisfies the brief's escape clause ("only if strongly justified"
for a JSON metadata cache).

### 3.4 File identity — **(path, size, CRC32) tuple from ZIP metadata**

**Decision**: A file in an incoming Takeout is identified by the tuple:

```
(normalized internal path, uncompressed size, CRC32)
```

- **path** — internal ZIP path, normalized to forward slashes, lowercased only for
  comparison key (the original mixed-case path is preserved in storage),
  trailing-slash trimmed.
- **size** — `UncompressedSize64` from the ZIP central directory.
- **CRC32** — `CRC32` from the ZIP central directory, as provided by Google's
  build of the ZIP (Google computes CRCs; we trust them the same way we trust any
  produced archive's metadata).

Comparison rules:

| incoming path | incoming (size,CRC) vs existing entry under that path | result |
|---|---|---|
| matches an entry | identical (size, CRC) | **unchanged → skip** (`Skipped files++`) |
| matches an entry | size or CRC differs | **modified → append under versioned name** (`Modified files++`) |
| no entry under that path | — | **new → append under original name** (`New files++`) |
| no entries at all | — (first sync) | append everything |

**SHA-256 is NOT used for identity.** It is used only:

1. To verify downloaded binaries at install/update time (best practice).
2. Optionally to integrity-check the consolidated archive via `verify` (re-read every
   payload, recompute CRC32 — SHA-256 not even needed here; CRC32 + size already
   covers it; if a maintainer wants strong integrity, `verify --deep` can SHA-256 the
   central directory and the payloads, but this is opt-in).

Rationale: every better-than-CRC32 hash requires **decompressing payloads**, which
violates C5 (no massive extraction) and is redundant because ZIP integrity is
already CRC32-protected for every entry and the spec mandates compliant readers
verify CRC32. CRC32 collisions are astronomically unlikely at this scale (~10^5
to 10^7 files historically, with size also compared); the brief explicitly permits
this strategy.

### 3.5 Versioning scheme — **plain name = original; later versions suffixed**

**Decision**: The **first appearance** of a path is stored under its **original
unmodified name** (`Photos/image.jpg`). Each subsequent **modified** version is
appended under a deterministic suffix **immediately before the final extension**:

```
Photos/image.jpg            (oldest version, original name kept literal)
Photos/image__v2.jpg        (2nd version found)
Photos/image__v3.jpg        (3rd version found)
Photos/image__v4.jpg        (4th version found)
```

Rules:

- Insert `__v<N>` immediately before the **last** `.` in the basename.
  Example: `Photos/image.V1.raw.jpg` → `Photos/image.V1.raw__v2.jpg`.
  If no extension, append at the end: `README` → `README__v2`.
- For paths containing `.` in directory names, only the **basename** is modified
  (dir names untouched).
- Counter `<N>` is computed from the central directory at sync time by counting
  existing suffixed versions of that exact path. Deterministic, no hidden sequence.
- All suffix characters (`_`, letters, digits) are valid in ZIP entries and
  on both Windows and Linux filesystems. **No colons, no backslashes, no
  reserved Windows chars**. Zippers and OS unzip tools handle these names as
  plain filenames.
- The plain name never changes once stored — the original entry is **never
  renamed** in the central directory, which further protects append-only
  semantics (existing central-directory records are stable).

This is **human-readable** (a user can browse `Photos/` and see image.jpg plus
image__v2.jpg listing the history), gives an immediate "current/original at the
plain name" mental model, batch-runs cleanly and is deterministic.

**Alternative considered**: plain name = newest, older versions suffixed —
rejected because it requires renaming an existing central-directory entry on
every modification (more state churn, hurts the "never touch existing records"
property, complicates recovery).

### 3.6 Deleted files

Trivially handled: the archive is append-only and **we never delete entries**.
If a file disappears from future Takeouts, the previously-appended entries
remain. The `verify` command can additionally list entries that have not been
seen in any incoming Takeout during the last N syncs if the maintainer wants
that report, but **no deletion ever happens**. This satisfies C11 exactly.

### 3.7 Portable binaries hosted on **GitHub Releases**

**Decision**: Native binaries are NOT committed to the repository and are NOT
stored via Git-LFS. They are **GitHub Release assets** under a pinned tag, fetched
by the installer and the self-updater.

Rationale (choosing among the three named strategies):

| Strategy | Pros | Cons | Verdict |
|---|---|---|---|
| **GitHub Releases assets** | Not bloating repo history; reproducible/pinned per tag; stable public CDN URLs (`https://github.com/<owner>/<repo>/releases/download/vX.Y.Z/<asset>`); trivial SHA-256 verification `download` + `.sha256` sibling; works offline once cached | None significant for this use-case; bandwidth cost only at install | **Recommended** |
| Binaries committed in repo | Trivial clone = ready | Repo bloats forever; clone slow; history rewrites hard | Rejected (violates the spirit of "single maintainer, lean repo") |
| Git-LFS | Stays in repo view | Adds LFS client dependency; some clones (e.g., shallow, GitHub archive downloads) won't include LFS objects; complicates the **offline clone** use-case | Rejected (hurts C2 and reproducibility) |

Asset naming:

- `takeoutback-linux-amd64`  ← ELF64, `CGO_ENABLED=0`
- `takeoutback-windows-amd64.exe`  ← PE64, `CGO_ENABLED=0`
- `takeoutback-linux-amd64.sha256`  ← checksum
- `takeoutback-windows-amd64.exe.sha256`

A `VERSION` plain-text file in the repo records the latest stable tag for tooling
/checks. A `RELEASE_CHECKSUMS.txt` accompanies each release.

### 3.8 Update mechanism

`--update` (cli) or menu item 4:

1. Read local `VERSION` / build-time version string.
2. Query `https://github.com/<owner>/<repo>/releases/latest` (follow redirects to
   resolve the latest semver tag). Honor `HTTP_PROXY`/`HTTPS_PROXY` if set; pure
   stdlib via `net/http`.
3. Compare semver. If `≤` local, report "already up-to-date" and exit 0.
4. Download new binary for the current `GOOS/GOARCH` to a temp file inside
   `TakeOutBack/tools/<platform>/.tmp/`, verify SHA-256 against the release
   checksum, fsync.
5. Atomic install: `os.Rename` over the existing `takeoutback` (atomic on
   Windows ≥Vista and on POSIX *for same volume*, which it is — same directory).
   On Windows, where you cannot overwrite an executing binary directly, the
   launcher copies itself aside or we keep two slots `takeoutback.exe` and
   `takeoutback.exe.next` swapped by a small staging indirection. ( concrete
   recipe in §8. )
6. Optionally refresh `TakeOutBack.sh` and `TakeOutBack.bat` from the release.
7. On network failure: report "offline, cannot check for updates", exit 0.

The update **never touches** `Archive/`, `Incoming/` or `config/state.json`
user data.

---

## 4. Alternative Designs Considered and Rejected

### 4.1 Multi-volume ZIP set (split archives)

Idea: split `Consolidated.zip` into `Consolidated.zip.001`, `.002`, ...
(4 GB each) to dodge ZIP64 concerns and filesystem limits.

- Pros: Each volume stays below any FAT32/exFAT size limits; easier to copy on
  filesystems hostile to large files.
- Cons: More state to keep consistent; one corrupt volume jeopardizes the whole
  logical archive; compaction/rebuild spans many files; cross-tool compatibility
  of split zips is decent (7z/WinRAR) but uneven (Windows Explorer can't open
  split zips natively). More complex than needed.

Verdict: **Rejected for now.** Use ZIP64 inside one archive — ZIP64 is required
beyond 4 GB / 65535 entries and is universally supported by every modern unzip
including Windows Explorer (Windows 10+ opens ZIP64 fine). If a user's archive
ever exceeds **practical single-file limits** (exFAT max file size is 16 EiB,
NTFS ~8 PB; both non-issues) we would revisit. Documented as a future option only.

### 4.2 Full archive rebuild every sync

Idea: Read existing `Consolidated.zip`, write a new `Consolidated.zip.new` from
scratch including all new files, fsync, atomic rename.

- Pros: Simplest mental model; trivially consistent.
- Cons: For users with multi-GB / multi-TB archives, rewriting the whole archive
  every sync is **catastrophic** for an external SSD/HDD over USB (could take
  hours and enormous writes, shortening drive life). Violates "minimize disk
  writes" and "avoid expensive rebuilds". Defeated by C5.

Verdict: **Rejected** as the operational path. **Used only as a rare, opt-in
"compaction" step** (`--compact`) to prune orphaned central directories that
accumulate from append-only CD rewritings (see §7.4).

### 4.3 Content-addressable store + thin ZIP view

Idea: Store each unique file once by SHA-256 in a CAS directory; present a virtual
ZIP as a manifest.

- Pros: Optimal dedup.
- Cons: Two systems (CAS + ZIP), non-ZIP storage backend violates **C3 ("the
  complete solution shall be ZIP based")**; cross-platform fidelity lost; much
  more complex; harms maintainability.

Verdict: **Rejected** (violates C3 and simplicity).

### 4.4 SQLite index as the source of truth

- Pros: Fast queries.
- Cons: Explicitly forbidden by C4.

Verdict: **Rejected.** Allowed only as an in-memory build structure that is never
persisted; the persistent store is ZIP-derived.

### 4.5 Running as a daemon / scheduled task / service

- Pros: Background syncs.
- Cons: Explicitly forbidden (no servers, no daemons, no services, no scheduled
  tasks). The brief is explicit.

Verdict: **Rejected.** Sync is always an explicit user action via the launcher.

### 4.6 History via `.bak` files outside the ZIP (filesystem versioning)

Idea: store old versions as `image.jpg.bak` in the host filesystem.

- Cons: Violates "everything lives inside ZIP", spreads state across two systems,
  loses cross-host portability (host filesystem differences).

Verdict: **Rejected** (violates C3).

---

## 5. ZIP Constraints Analyzed

This section addresses the explicit asks of the brief about ZIP limits and
whether a single-giant-archive stays viable.

| Limit | Classic ZIP | ZIP64 | TakeOutBack impact |
|---|---|---|---|
| Archive size | 4 GiB | ~16 EiB | ZIP64 fine |
| Single uncompressed entry | 4 GiB | ~16 EiB | ZIP64 fine |
| Single compressed entry | 4 GiB | ~16 EiB | ZIP64 fine |
| Entry count | 65535 | 2^64 | ZIP64 fine |
| Central directory size | ~64 KiB (comment limit) | ZIP64 uses separate 8-byte fields; 64-bit counts | ZIP64 fine |
| End-of-Central-Directory comment | 65535 bytes | same | We keep zero comment |
| Path encoding | IBM CP437 default, UTF-8 via the "Language Encoding Flag" (EFS, bit 11) | unchanged | We always write UTF-8 with EFS set |
| Path separator | `/` mandatory per spec | unchanged | Forward slashes everywhere |
| Timestamp | MS-DOS (2-second granularity, max 2107) | unchanged; "Extended Timestamp" extra field (UT) optionally provides UTC | We write MS-DOS and emit UT extra field for UTC precision |

**Conclusion:** a single ZIP64 archive is viable well beyond realistic
multi-decade personal-scrape sizes (single huge videos may exceed 4 GiB;
ZIP64 handles them; Windows Explorer opens ZIP64 archives containing
>4GB single entries on Win10+). The Go stdlib emits ZIP64 records
**automatically** when any threshold is exceeded — no special code path
needed beyond letting `zip.Writer` grow.

Anti-corruption properties used:

- Each local file header carries its own CRC32, so partial corruption is
  detectable per-entry by `--verify`.
- The central directory is a manifest: if the file got truncated, the surviving
  central directory to that point is coherent and recoverable.
- Our recovery re-scans local file headers across the file body, which requires
  only that each LFH is self-describing (it is: signature + version + flags +
  method + CRC + size + name). Uncompressed size with bit-3 set means data
  descriptor follows. We will **always** write `flags bit 3 = 0` for streamed
  data to put the CRC and sizes in the LFH (we know them upfront from the source
  archive), making **parse-from-LFH always possible** even without the central
  directory.

That last point is operationally crucial and is the cornerstone of the recovery
story: **we never emit streamed entries** (`bit 3 = 0`), every LFH is complete
on its own, so the file body is **self-describing**.

---

## 6. Synchronization Algorithm

### 6.1 Inputs

- `Incoming/` containing one or more Google Takeout ZIP files (any names).
   Google Takeout ZIPs may be multi-part (`takeout-001.zip`, `takeout-002.zip`,
   ...) but each ZIP is internally self-contained; we treat them as independent
   archives. We do NOT attempt to merge a "logical" multi-part set because
   Google's **TAR multi-volume** numbering is just a sequential split, while the
   **ZIP** export format Google offers yields independent ZIPs.
- `Archive/Consolidated.zip` (created on first sync).

### 6.2 Step-by-step

1. **Startup health check**
   - Resolve project root from where the launcher lives; verify `Incoming/`,
     `Archive/`, `TakeOutBack/{tools,config,logs,temp}` exist; create missing.
   - Load `config/Consolidated.zip.state.json` (if missing, set `is_first=true`).
   - Open `Archive/Consolidated.zip` if it exists. Verify its current EoCD/CD
     integrity (compare `cd_sha256` in state file vs actual CD bytes).
     - If consistent → proceed.
     - If inconsistent → run **Recovery** (§9) before doing anything else.
       Recovery may take time; log it; never auto-write until recovered.
   - Load the existing index `IndexExisting` = `path → [(version_index, size, crc32, cd_offset, method)]`
     from the **last good central directory** (fast: parse CD only).

2. **Discover incoming archives**
   - Glob `Incoming/*.zip` (case-insensitive on Windows, still okay on Linux).
   - For each candidate, probe by opening with `zip.OpenReader` (Go) — if it
     fails to read a central directory, log a **warning** and skip (don't crash
     on a half-downloaded Takeout). This is the validity test.
   - Print a numbered list of the archives that will be processed.

3. **Build incoming index per archive**
   - For each valid Takeout ZIP `T`:
     - Walk the central directory entries.
     - Filter out Google Takeout's own utility entries (e.g. `archive_browser.html`,
       `index.html` of the export, `.*.json` metadata sidecars) — the maintainer
       decides policy via `config/policy.json` (default: keep all files; the
       filter is OPT-IN and OFF by default to honor "preserve everything").
      - Build `IndexIncoming_T` = `normalized_path → (size, crc32, method, cd_offset_in_T, compressed_size_in_T, flags)`.
      - Render a per-archive progress bar on the console while entries are
        processed.

4. **Diff & classify**
   - For each entry `(p, s, c)` in `IndexIncoming_T`:
     - If `IndexExisting` does not contain `p` → **NEW**. Append candidate list
       as `(p, src_archive=T, src_offset=cd_offset, kind=NEW)`.
     - Else let `existing_head` = the **plain-name** entry for `p` (the original
       one). Compute `(s_existing_head, c_existing_head)`:
       - If `(s, c) == (s_existing_head, c_existing_head)` → **UNCHANGED**
         (skip; counter++).
       - Else → **MODIFIED**. Candidate appended as
         `(suffixed_name_using_count, src_archive=T, src_offset=cd_offset, kind=MOD)`.
       - The "max version count" for the suffix is taken across all entries for
         that `p` already in `IndexExisting` (count `__v<N>` occurrences and the
         literal plain entry = v1). The new entry gets the next counter value.

5. **Append payloads (raw deflate copy)**
   - Acquire a single-byte lockfile `Archive/.consolidated.lock` (atomic create
     with `O_EXCL`) to prevent two TakeOutBack instances interfering.
   - Open `Consolidated.zip` in `O_WRONLY|O_APPEND` (or r+b to allow later
     backpatch — not required because we never read-modify-write LFHs).
   - For each append candidate:
     - Open the source entry from `T` via `zip.OpenReader(T)`; locate the same
       entry by its central-directory index; copy its **compressed bytes** from
       the source archive into the destination via an `io.CopySection` (we know
       the compressed-data offset: `cd_offset_to_local` + `LFH_size`; LFH is
       parsed to know its size, no need to compute from spec each time — Go's
       `zip.Reader` exposes `File` but not raw offset; we use `(*zip.Reader).OpenRaw`
       style by reading the LFH ourselves to get offset/size). Write a new LFH
       in the destination with the **same** method, **same** CRC32, **same**
       uncompressed/compressed sizes, the **same** extra fields (normalized),
       same UTF-8 path (with our suffix if MOD). Stream-copy the deflate payload.
       - Result: byte-exact payload (identity preserved), zero recompression.
       - **Corner case**: `Method = Store` entries can also be re-deflated to
         save space optionally — **off** by default (we preserve bytes). We
         document the toggle in `policy.json`.
     - After each entry appended, optionally progress-sync to disk (we batch:
       fsync after each Takeout-file worth of entries).
   - Keep a list of `[(orig_dst_path,LFH_offset,method,crc,size)]` to build the
     central directory from.

6. **Append new central directory + new EoCD**
   - Compute `cd_offset` = current file size after all new payloads appended.
   - Write the new central directory containing entries for **all** known files
     (old entries + new entries) — but note: since we're append-only and the old
     central directory still physically exists earlier in the file, the **new**
     central directory supersedes it. After this writer is done, the file ends
     with: `[payloads...][new CD][new EoCD]`.
   - Implementation detail: rather than recomputing the **full** central
     directory from scratch by scanning every old LFH (slow on a huge archive),
     we keep the **list of all prior CD records** in the sidecar/in memory by
     parsing the prior CD once at startup (cheap — CD is small relative to
     payloads). We then **append only the added CD records** conceptually, but
     since CD must be contiguous and followed by exactly one EoCD referencing
     its start, we must write the **full** CD at the new offset. We do this by:
     - At startup, we read the **bytes of the existing CD** into memory (parse
       CD records into `[]cd_record`, plus keep a raw byte copy for fast append).
     - Append the new CD records (only the appended entries since last sync).
     - Then **append the entire existing CD bytes + new CD records** at the end
       of the file. The **new EoCD** points to this freshly-written contiguous
       CD block. The old CD bytes are now dead weight in the middle of the file
       (pruned during compaction, §7.4).
   - Write EoCD with `_cd_total_entries`, `_cd_size`, `_cd_offset`; if we exceed
     classic ZIP limits, Go's `zip.Writer` will emit ZIP64 EoCD automatically
     **only if** we route through its writer — but we are hand-writing the CD
     for surgical control. Therefore we **emit ZIP64 records ourselves** when the
     chain crosses thresholds (`> 0xFFFF` entries, `> 0xFFFFF_FFF` offsets, etc.);
     see §7.5 for the exact ordering (Zip64 EOCD locator + Zip64 EOCD + EOCD).
   - **Final fsync**.

7. **Finalize state sidecar atomically**
   - Build new state JSON `state.new.tmp`. `validate_against_live_archive`.
   - `write tmp → fsync → os.Rename(tmp, state.json)` atomic.
   - Update `Consolidated.zip.cd.bak` with the last good CD bytes (atomic same way).
   - Release lockfile (close + unlink).

8. **Logging & reporting** — write `logs/YYYY-MM-DD.log`; print the summary.

### 6.3 Worst-case complexity

- Per sync: O(E_in) incoming entries to classify + O(E_in) appends + O(E_total)
  to write one new contiguous CD block.
- CD rewrite cost = O(E_total) bytes per sync (the unavoidable cost: ZIP's CD
  is its index). For 10^6 historical entries, CD ~ 100–200 MB at most — easily
  written in a second on USB 3 + SSD. Acceptable.
- Payload cost = only NEW + MOD payloads (the design's main efficiency win).
- No full-archive rewriting, no full decompression.

---

## 7. ZIP Update Strategy — Detail

### 7.1 Closure model & file ending

End-of-file layout after a successful sync:

```
... existing payloads ...
... new payloads (this sync) ...
[ dead old Central Directory bytes (until compaction) ]
[ new full Central Directory bytes ]
[ Zip64 End-Of-Central-Directory record (if needed) ]
[ Zip64 EOCD Locator (if needed) ]
[ EOCD record ]
```

The **EOCD at the very end** is what spec-compliant readers find by scanning
backward from EOF (within ~64KiB + its comment).

### 7.2 Why this is crash-safe

- **Only append** ever mutates the file. We never overwrite previously-written
  known-good bytes except the rare compaction step.
- Between syncs the file ends with a stable, valid `[old CD][old EOCD]`
  (and old payloads before).
- During a sync we are **only appending** bytes. At any instant of failure:
  - If killed before writing the new EOCD (i.e., during new payloads/new CD): the
    last valid EOCD is NOT the trailing bytes anymore (EOF moved), but the old
    EOCD **physically exists** inside the file but no longer at EOF position.
    Standard readers scan the **last** ~64KiB for EOCD signature with comment
    matching; with a 0-byte comment, the old EOCD has been displaced forward
    from EOF. **Readers cannot auto-find it**. → file appears invalid.
  - Therefore **our recovery (§9)** truncates the file back to the **last known
    good `archive_end`** recorded in the sidecar, which places the old EOCD back
    at EOF and restores a consistent archive. This is safe because the appended
    bytes between `archive_end` and current EOF are simply thrown away (they are
    copies of data still present in the incoming Takeout ZIP, which remains in
    `Incoming/` until the user removes it — by policy we keep incoming files
    until the user explicitly archives/deletes them, optionally after a verified
    successful sync).
  - If killed *after* writing a complete new EOCD but *before* updating the
    state sidecar: on next startup we recognize `state.cd_offset` may not match
    what the file claims; we **trust the file** (rebuild state sidecar from the
    trailing valid EOCD/CD scan). Either source of truth is recoverable.
- The two sidecars (`state.json` + `cd.bak`) are tiny and renamed atomically
  (Windows-safe rename-in-same-dir). Their loss degrades only performance
  (startup must rebuild from a full LFH scan), not correctness.

### 7.3 Damage-controlled operations

- We always write LFH + payload + their in-LFH CRCs/sizes (never streamed):
  data descriptors would force forward-parsing on recovery. Refusing streaming
  is a one-line documentation requirement and the **core** of C6.
- We always `Sync()` (fsync) before renaming state sidecars and considering the
  operation successfully reported to the user.

### 7.4 Compaction (`--compact`, opt-in)

Over many syncs, dead CD blocks accumulate. `--compact` rewrites the archive:

- Snapshot current valid CD in memory.
- Stream-copy all referenced payloads (raw bytes) into `Consolidated.zip.compact.tmp`.
- Write a single new CD + EoCD.
- Fsync, atomic `os.Rename` over `Consolidated.zip`.
- Update sidecars.
- Always recovers full consistency.

This is the **only** operation that rewrites the archive (and it is opt-in and
infrequent). Because every payload is byte-copied without decompression it's
still cheap compared to a generic extract+rezip.

### 7.5 ZIP64 emission sequence (when limits crossed)

If `total_entries > 0xFFFF` or `cd_offset_or_size > 0xFFFFFFFF`:

1. Write Zip64 EOCD record (signature `0x06064b50`) with 64-bit fields.
2. Write Zip64 EOCD Locator (signature `0x07064b50`) pointing to the Zip64 EOCD.
3. Write classical EOCD with sentinel `0xFFFFFFFF` values into the 32-bit fields.

Go stdlib's `zip.Writer` does this reproducibly; we route through `zip.Writer`
configured with a custom writer that we wrap to ensure the **CD bytes** we
precomputed in memory are flushed identically (i.e., we don't actually use
`zip.Writer` for the **payload** phase because we need raw byte copy; we use it
**only for the final CD+EOCD construction** by calling `Writer.Create` with zero
bytes for each entry to produce exactly the CD bytes — or, simpler, we hand-write
CD records in our own small helper library, calling stdlib types for reference.
Final implementation detail deferred to coding phase.)

### 7.6 Atomic rename on Windows

`os.Rename` is atomic and replaces existing target **since Go 1.5+ on Windows via
`MoveFileEx(move|replace)`**. We rely on this for state sidecars and `--compact`.

---

## 8. Cross-Platform Consistency

To guarantee a Linux-produced archive opens on Windows and vice-versa:

- **Paths in ZIP entries use forward slashes** — ZIP spec requirement.
- **UTF-8 path encoding** with the EFS flag (bit 11) set.
- **UTC DOS timestamps** + Extended-Timestamp (UT) extra field with `mtime_mod`
  only (no atime); no platform-specific extra fields.
- **No symbolic links stored** (Takeout never produces them).
- **No case-only renames** detected on sync: paths are compared **case-insensitively**
  on Windows for safety and the actual stored path keeps its original case from
  the incoming archive — but the dedup key on Windows is lowercased to avoid
  `Foo.jpg` / `foo.jpg` clashes that Windows would treat as the same file.
  Linux preserves case (`Foo.jpg` ≠ `foo.jpg`).
  - To keep cross-platform behavior consistent (a hard requirement), we adopt the
    **safer Windows-like** rule globally: the dedup key is lowercased-cased+path,
    but the **stored name** keeps the original incoming case. This means on Linux,
    `Foo.jpg` and `foo.jpg` arriving in different Takeouts would be treated as the
    same path (first-wins controls the stored case); future MODed versions get
    `Foo.jpg__v2`, etc. This prevents subtle, surprising Windows-Linux divergent
    history. It's a documented, conservative choice.
   - **Launchers** are platform-specific thin wrappers:
   - `TakeOutBack.sh` — `#!/usr/bin/env bash`; resolves its own dir; calls
     `<dir>/TakeOutBack/tools/linux/takeoutback "$@"`.
   - `TakeOutBack.bat` — resolves its own directory and calls
     `TakeOutBack\tools\windows\takeoutback.exe --root "<dir>." %*`. The trailing
     `.` is appended to the directory so that `"F:\"` (which would escape the
     closing quote in batch) becomes `"F:\."`, a valid path that Go normalizes
     back to `F:\`.
   Identical CLI behavior underneath; both use forward-slash-agnostic Go.
- **No backslashes ever** in stored ZIP data; the Go engine never constructs
  ZIP paths from OS-specific separators.

---

## 9. Recovery & Fault Tolerance

Operating principles:

- Append-only archive.
- Complete LFHs (no streaming) so the file body is self-describing.
- Tiny sidecars are written `tmp→fsync→rename`.
- A lockfile prevents concurrent runs.
- Any interrupted run is detected on the next startup.

Recovery decision tree (run automatically before any sync):

```
open Consolidated.zip ->
  read sidecar state.json and cd.bak (may be missing) ->
  scan trailing 64 KiB + 22 for EOCD signature with comment == 0 ->
  if found:
     CD at eocd.cd_offset, size eocd.cd_size, entries eocd.cd_total =>
     compare to sidecar (cd_offset+cd_size+cd_sha256) :
       match     -> HEALTHY
       mismatch  -> state sidecar went stale/forged ->
         cd.bak has valid bytes at cd_offset? if yes -> trust cd.bak
         else recompute CD from a full LFH scan (slow path) -> SUCCESS
  if not found:
     file truncated mid-write ->
       truncate_to_last_known_good_end (= sidecar.archive_end) ->
       re-validate EOCD ->
       if still fails -> full LFH scan; rebuild a CD; write it; update sidecars
```

Power-failure / force-quit / USB-yank modes covered:

| Crash point | Result on next run |
|---|---|
| Before first payload appended | Old archive untouched; nothing to do. |
| Mid-append of a payload | Truncate to `archive_end` recorded in sidecar; sync resumes from incoming. |
| After payloads, before CD | Same truncation; we simply re-add those entries next sync. |
| Mid-CD write | Truncation; rerun. |
| After CD, mid-EOCD write | Truncation; rerun. |
| After EOCD, before sidecar update | On startup, file is consistent; we **derive** sidecar from the file's trailing CD and proceed; sidecars auto-overwritten. |
| Mid-sidecar write (rename aborts) | The atomic rename means EITHER old OR new sidecar exists; never a partial file. Either is usable. |

**No corruption ever survives**: the worst case is "wasted space and a needed
compaction". The smart invariant is **we never overwrite known-good committed
bytes**, so we can always roll back to them.

### 9.1 Optional `.bak.shield` strategy

For extra paranoia we can additionally keep a SHA-256 of the *payload bytes of
the last good realization* to detect silent bitrot (cosmic rays / failing USB
controller) on long-lived SSDs — this is exactly what `verify --deep` does on
demand; it is too expensive to do on every sync.

---

## 10. Directory Structure (final)

```
<ProjectRoot>/
├── TakeOutBack.sh                    # Linux launcher
├── TakeOutBack.bat                   # Windows launcher
├── Incoming/                         # user drop folder for new Takeout ZIPs
│   └── (takeout*.zip)
├── Archive/
│   ├── Consolidated.zip              # the master append-only archive
│   ├── Consolidated.zip.state.json   # cache/recovery sidecar (NOT user data)
│   ├── Consolidated.zip.cd.bak       # last-good CD backup (NOT user data)
│   └── .consolidated.lock            # cross-run mutex (NOT user data)
└── TakeOutBack/
    ├── app/                          # <reserved> application support files
    ├── tools/
    │   ├── linux/
    │   │   └── takeoutback           # native binary (Linux x86_64)
    │   └── windows/
    │       └── takeoutback.exe       # native binary (Windows x86_64)
    ├── temp/                         # transient scratch (compaction, extract probes)
    ├── logs/
    │   └── YYYY-MM-DD.log
    ├── config/
    │   ├── settings.json             # paths, log level, policy flags
    │   ├── policy.json               # optional entry filters (default = keep all)
    │   └── VERSION                   # single line: vX.Y.Z (app version)
    ├── docs/
    │   ├── README, Architecture.md, Installation.md, Usage.md,
    │   ├── Development.md, Troubleshooting.md
    ├── scripts/                      # operational scripts (e.g. self-update)
    │   ├── install.sh
    │   ├── install.ps1
    │   ├── self-update.sh
    │   └── self-update.ps1
    └── tools/<platform>/.tmp/        # download staging used during updates
```

User-facing surface is **only** `TakeOutBack.sh`, `TakeOutBack.bat`, `Incoming/`
and `Archive/` per the brief.

---

## 11. Installer Design

### 11.1 Trigger

Linux (any shell with bash):

```sh
curl -fsSL https://github.com/<owner>/<repo>/releases/download/vX.Y.Z/install.sh | bash
# or, picking a version:
TAKEOUTBACK_VERSION=v0.1.0 curl -fsSL <url>/install.sh | bash
```

Windows (PowerShell 5.1+, default on Win10/11):

```powershell
irm https://github.com/<owner>/<repo>/releases/download/vX.Y.Z/install.ps1 | iex
```

A default `latest` URL can also be provided but **pinned tags are the
recommended path** for reproducibility.

### 11.2 Behavior

Both installers:

1. Detect host `OS_ARCH` (uname on Linux; `$env:PROCESSOR_ARCHITECTURE` on Windows).
2. Resolve a target directory: `./` (current dir is empty, as required). The
   installer **refuses to run into a non-empty folder** unless `--force`/`-Force`
   is passed (protect against wiping).
3. Create the directory structure (§10) and an empty `Archive/` and `Incoming/`.
4. Fetch:
   - the matching binary (`takeoutback-linux-amd64` or
     `takeoutback-windows-amd64.exe`),
   - its `.sha256` checksum sidecar,
   - the copy of `TakeOutBack.sh` ·/· `TakeOutBack.bat`,
   - `docs/*.md`,
   - `config/settings.json` + `config/policy.json` defaults,
   - the launcher scripts.
5. Verify each downloaded file against its checksum; abort on mismatch.
6. Place executables in `TakeOutBack/tools/<platform>/` (actually only the
   **current host**'s binary is required; the launcher never needs the other
   platform's binary unless the user explicitly wants both via
   `--both=true`).  For true cross-platform copy-to-an-external-drive scenarios
   the installer running on either OS can ALSO pull the opposite-platform binary
   by setting `TAKEOUTBACK_FETCH_BOTH=1` (default **on** for the external-drive
   use case so the same folder works on both OSes after a single install).
   This is the key convenience hook to satisfy "execute from USB on either OS".
7. Write `TakeOutBack.sh` (POSIX LF line endings via `printf`, portable enough)
   and `TakeOutBack.bat` (CRLF) so each OS's launcher works.
8. Write `config/VERSION`, `config/settings.json`, `config/policy.json`.
9. Print final instructions. No PATH writes, no env vars, no services.

### 11.3 Idempotency & upgrades via install

Re-running the installer into an existing folder **upgrades** the binary and
scripts **without touching** `Archive/`, `Incoming/` or `config/state.json`.
Files that exist are kept; missing files are (re)created. This makes `|
bash`/`| iex` also a self-repair / upgrade path.

---

## 12. Logging, Reporting & CLI

### 12.1 Logs

- One file per calendar UTC day: `TakeOutBack/logs/YYYY-MM-DD.log`.
- Rotation policy: keep last 90 days (configurable). Each line is a single JSON
  object (machine-parseable) with a human-readable text rendering for `tail`.
- Per-sync metadata captured: start time, end time, duration, archives scanned,
  files scanned, new files, modified files, skipped files, bytes appended,
  warnings, errors, recovery-actions-taken.

### 12.2 Live progress and console summary

During sync the console shows:

1. A numbered list of archives about to be processed.
2. A per-archive ASCII progress bar redrawn in place (`\r`) showing entries
   processed and percentage.

Example:

```
Archives to process: 4
  1. takeout-2025-001-of-001.zip
  2. takeout-2025-001-of-002.zip
  3. takeout-2025-001-of-003.zip
  4. takeout-2025-001-of-004.zip
  [===============================>] takeout-2025-001-of-001.zip 1542/1542 (100%)
  [===========>                  ] takeout-2025-001-of-002.zip  623/1823 (34%)
```

When the run completes, the final summary is printed:

```
TakeOutBack vX.Y.Z
Archives scanned : 4
Files scanned    : 182345
New files        : 523
Modified files   : 12
Skipped files    : 181810
Bytes appended   : 1.42 GiB
Duration         : 00:02:14
Status           : OK
```

### 12.3 CLI surface

```
./TakeOutBack.sh                       # default: synchronize
./TakeOutBack.sh sync                  # explicit sync
./TakeOutBack.sh verify                # integrity verify (CRC check + size check)
./TakeOutBack.sh verify --deep         # also re-hash payloads (opt-in)
./TakeOutBack.sh stats                 # show archive statistics
./TakeOutBack.sh compact               # prune dead central directories
./TakeOutBack.sh update                # self-update binary 
./TakeOutBack.sh menu                  # interactive TTY menu (1..5)
./TakeOutBack.sh --version
./TakeOutBack.sh --help
```

Interactive menu (when run with TTY):

```
1. Synchronize
2. Verify archive
3. View statistics
4. Update tools
5. Exit
```

Exit codes: 0 OK, 1 operational error, 2 recovery required (manual run
`verify`), 3 update failed/offline, 4 lock held by another instance.

---

## 13. Git / GitHub Workflow

(macros and statements, **subject to maintainer's final repo config** — see §14)

### 13.1 Branching (recommendation)

- `main` — always shippable, each commit tagged eventually; what releases are
  cut from.
- `feat/*` / `fix/*` / `docs/*` — short-lived feature branches off `main`, merged
  via squash-merge PR (linear history) or rebase+fast-forward (clean history).
- Optional `dev` integration branch only if multiple contributors ever appear.

### 13.2 Commit discipline

- Atomic commits; one logical change each.
- Conventional Commits prefixes:
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `perf:`, `test:`, `build:`,
  `ci:`.
- Subject ≤ 72 chars; body explains *why*, not *what*.

### 13.3 Tags / Releases

- Semantic versioning: `vMAJOR.MINOR.PATCH`.
- Each `main` release commit → an annotated git tag `vX.Y.Z`.
- GitHub Release for each tag, with:
  - Changelog entry (auto-generated from commit log + manual edits).
  - Attached assets: `takeoutback-linux-amd64`, `.sha256`,
    `takeoutback-windows-amd64.exe`, `.sha256`, complete docs bundle, the
    `install.sh` + `install.ps1` referenced by URL.
- No binaries committed to the source tree.

### 13.4 CI (when the maintainer enables Actions)

- `on: push to main` and `PR`:
  - `lint` (`gofmt`, `go vet`, `staticcheck`).
  - `test` (`go test ./...`).
  - `build` cross-compiled artifacts for both OSes; with `-trimpath -ldflags
    "-s -w"`.
- `on: tag push`:
  - Build + compute SHA256 + sign (optional) + `gh release create vX.Y.Z ... .

### 13.5 CHANGELOG.md

Keep-a-Changelog format. Top-level sections per version:
`Added`, `Changed`, `Fixed`, `Removed`. Linked from README and releases.

### 13.6 Documentation set (in `docs/`)

`README.md` (top-level), `Architecture.md` (this), `Installation.md`, `Usage.md`,
`Development.md`, `Troubleshooting.md`. All in English.

---

## 14. Decisions Log

These decisions were made during implementation and are reflected in the
released code:

1. **Repository**: `gukak/GoogleTakeOutBack` on GitHub, public, `main` branch
   with short-lived feature branches, HTTPS push.
2. **Runtime**: **Go static binary (Option A)** — single binary per OS, stdlib
   only, cross-compiled from one source tree.
3. **Policy defaults** (all configurable in `TakeOutBack/config/policy.json`):
   - Keep Google Takeout's metadata HTML/JSON sidecars (default: yes — keep all).
   - Drop incoming Takeout ZIPs after a verified sync (default: no — keep).
   - Re-deflate `Store` entries to save space (default: no).
   - Auto-compact on a threshold of dead-CD bytes (default: 0, opt-in).
4. **Asset signing**: not implemented; SHA-256 checksums are used for all
   release assets and verified by the installer and updater.
5. **Windows launcher quoting**: the batch file passes `--root "<dir>."` to
   avoid the `"F:\\"` quote-escape bug in `cmd.exe`.

---

## 15. Risk Analysis

| ID | Risk | Severity | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | Google changes Takeout's packaging (TAR-LZW, ZIP with non-deflate methods, chunk sizes) | High | Medium | ZIP central directory reading auto-handles deflate and store; for unusual methods we fall back to re-deflate copy (decompress + recompress) per entry — slower but always correct; documented policy. Detection + warning on unknown compression method. |
| R2 | Incoming archive paths collide only by case (Windows-vs-Linux subtlety) | Medium | Medium | Lowercase dedup key globally; keep original-case store name (§8). |
| R3 | CRC32 collision across distinct contents (different content, same path, same CRC by chance) | Low | Negligible (CRC32 has 2^32 values; with ~10^7 files and size also compared, realistic) | Optional `verify --deep --hash=sha256` is provided. If the maintainer ever requests, identity tuple can be upgraded to include a SHA-256 cached in sidecar — but default remains metadata-only per brief. |
| R4 | USB drive pulled mid-sync | High | Medium (real use-case) | Append-only invariant + tiny sidecar + startup recovery (§9). Worst case: redo of the in-progress sync. No corruption. |
| R5 | The single binary is replaced/lost by user mistake | High | Low | Installer is idempotent — re-running re-creates; config/data never touched. |
| R6 | Filesystem path/codepage on Windows rendering our UTF-8 paths as gibberish when browsing extracted files | Medium | Low | We always set EFS flag (UTF-8). Windows 10+ honors this. Old Win10 update required `Set-Process...` nothing — already fixed in modern Win10. |
| R7 | Over very long timescales, *Go ecosystem* moves (stdlib churn) | Medium | Low (Go's backward-compat policy is famously good) | Pin to released Go versions; choose a supported LTS-ish line at release time; CI to build with two Go versions to catch regressions. |
| R8 | Maintainer loses Go expertise → maintenance suffers | Medium | Low | The codebase is scoped to < ~3k LoC, stdlib-only, heavily documented. A successor can come up to speed in a day; the spec is in `Architecture.md`. |
| R9 | The "dead central directory" pile-up inflates archive size | Medium | High (over many syncs) | `--compact` documented; auto-compact option (default off). |
| R10 | A rare incompatible reader refuses ZIP64 | Low | Low | Document supported readers; offer `--compact --legacy` to rewrite with classic limits when below the thresholds (no-op when above). |
| R11 | Concurrent runs from two OSes on the same external drive (dual-boot scenario) | Medium | Low | Cross-OS lockfile (`Archive/.consolidated.lock`) using `O_EXCL` + PID + hostname; second instance aborts with exit 4; lock auto-released after stale-ness heuristic. |
| R12 | Google Takeout archive larger than 4GiB for a single internal entry | Low | Medium | ZIP64 supported at write time and verified at read time; documented. |
| R13 | Sensitive personal data in the archive (PII) | Medium | Certain | The archive is on an external drive owned by the user; we add a `--encrypt` future capability roadmap item (AES-2/LZW zip AES-WinZip-compatible) — out of scope for v1, documented in roadmap. |

---

## 16. Roadmap (v0.x ➝ v1.x)

- **v0.1.0** — Skeleton: installer + directory layout + stub binary with `--version`
  and `--help`; nothing destructive works yet. Tagged release.
- **v0.2.0** — Read-only sync engine without writing: it scans `Incoming/` and
  `Consolidated.zip` (if present), reports a *plan* (what would be NEW/MOD/SKIP).
- **v0.3.0** — Full append-only sync + sidecar + recovery + logs. First usable
  release.
- **v0.4.0** — `verify`, `stats`, interactive menu.
- **v0.5.0** — `update` self-updater + checksum verification + release automated
  via Actions (if maintainer enables).
- **v0.6.0** — `--compact`.
- **v1.0.0** — Locked API, full docs reviewed, fault-tolerance tested
  (kill -9 mid-sync), v1 release tag. Public Release with `note.json` release
  notes linked from CHANGELOG.
- **v1.x** — Optional AES-zip encryption (`--encrypt`); optional
  multi-volume/policy enhancements.

---

## 17. Summary of Why This Is the Simplest, Most Robust Solution

- **One storage format (ZIP)** satisfies the brief's hard constraint and
  collapses persistence to a single file.
- **Append-only** semantics make every bug-class that's storage-corrupting
  **impossible by construction**: we never overwrite known-good bytes; the worst
  case is wasted space, fixable by a one-command compaction.
- **Metadata-only identity** via (path, size, CRC32) avoids decompression
  entirely on the hot path; incoming payloads are **byte-copied** as already-
  compressed deflate streams — no recompression, no extraction, minimal CPU,
  minimal writes.
- **Self-contained binary** (Go, stdlib-only) gives portability with **zero
  runtime dependency** — the strongest possible form of "no install / no admin /
  no PATH" - exactly matching the brief.
- **Two tiny JSON sidecars** are cache + crash-recovery aids only; correctness
  always survives their loss — the archive is the source of truth.
- **Atomic rename + fsync** of sidecars and (for compaction) the archive itself
  is the only concurrency/preempt primitive needed; there is no DB, no WAL, no
  journal to manage.
- **GitHub Releases** for binaries decouple the heavy artifacts from the
  lightweight source tree; install and update scripts are equally simple
  `curl|bash` / `irm|iex`.
- **Documented algorithms** (sync, versioning, recovery, compaction) plus a
  compact stdlib-only codebase makes a **single maintainer** credible, and
  allows a successor engineer to ramp up in a single sitting.

The architecture therefore directly and provably satisfies the brief's stated
values — *simplicity, portability, robustness, transparency, maintainability,
long-term reliability* — with minimum **invented** machinery and maximum reuse
of the ZIP format and the language runtime.

---

**End of Architecture v0.1. Awaiting maintainer confirmation per §14 before any
implementation begins.**