# Development Guide

## Repository Layout

```
TakeOutBack/
├── src/                      # Go source code
│   ├── cmd/takeoutback/      # CLI entry point
│   └── internal/             # internal packages
│       ├── app/              # paths, config, logging, constants
│       ├── engine/           # sync, verify, stats, compact, recovery
│       ├── state/            # sidecar state file
│       ├── updater/          # GitHub Releases self-updater
│       └── zipx/             # low-level ZIP read/write
├── TakeOutBack/
│   ├── scripts/              # install.sh, install.ps1, self-update wrappers
│   ├── config/               # default settings and policy
│   ├── tools/                # native binaries (not committed)
│   └── ...
├── TakeOutBack.sh            # Linux launcher
├── TakeOutBack.bat           # Windows launcher
├── Makefile
└── .github/workflows/        # CI and release automation
```

## Prerequisites

- Go 1.26 or later
- make (optional, for convenience)
- zip/unzip (only for local smoke testing)

A local Go toolchain can be installed without admin rights:

```bash
# Example: install to ~/.local/go
curl -fsSL https://go.dev/dl/go1.26.5.linux-amd64.tar.gz -o /tmp/go.tar.gz
mkdir -p ~/.local && tar -C ~/.local -xzf /tmp/go.tar.gz
export PATH="$HOME/.local/go/bin:$PATH"
```

## Build

```bash
make build-all
```

Or manually:

```bash
cd src
go build -o ../TakeOutBack/tools/linux/takeoutback ./cmd/takeoutback
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o ../TakeOutBack/tools/windows/takeoutback.exe ./cmd/takeoutback
```

## Test

```bash
make vet
make test
```

Run the built-in smoke test:

```bash
make build-all
# See Makefile or create a test layout manually; smoke test is informal.
```

## Coding Conventions

- Use the standard library only; no third-party dependencies at runtime.
- Keep packages small and focused.
- Export identifiers only when necessary; prefer internal packages.
- Write idiomatic Go with meaningful doc comments on exported symbols.
- All user-facing text, comments and documentation are in English.

## Release Workflow

1. Update `src/internal/app/app.go` `Version` and `TakeOutBack/config/VERSION`.
2. Update `CHANGELOG.md`.
3. Update documentation (`README.md`, `docs/*.md`) and the installer scripts
   (`TakeOutBack/scripts/install.sh`, `TakeOutBack/scripts/install.ps1`) to
   reference the new version.
4. Commit with a conventional commit message, e.g. `feat: release v0.4.9`.
5. Tag: `git tag -a v0.4.9 -m "Release v0.4.9"`
6. Push the tag: `git push origin v0.4.9`
7. The GitHub Actions `release.yml` workflow builds binaries, generates
   checksums, creates a GitHub Release and uploads the assets.

## Git Workflow

- `main` is the release branch.
- Create short-lived feature branches from `main`.
- Use conventional commit prefixes: `feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `perf:`, `test:`, `build:`, `ci:`.
- Merge via pull request with a clean history.

## Owner/Repository Configuration

Before the first real release, change the placeholder owner/repository in:

- `src/internal/app/app.go` — `OwnerRepo`
- `TakeOutBack/scripts/install.sh` — `OWNER_REPO`
- `TakeOutBack/scripts/install.ps1` — `$OwnerRepo`

## Adding a New Command

1. Implement the command in `src/internal/engine/` or a new package.
2. Add the case to `src/cmd/takeoutback/main.go`.
3. Add the command to the interactive menu if appropriate.
4. Update `README.md`, `docs/Usage.md`, `docs/Architecture.md` and `--help` text.

## UI Guidelines

- Keep output human-readable and informative.
- During long operations (e.g. sync), show live progress: list the work to be
  done, then redraw a progress bar on the same terminal line with `\r`.
- Always end with a clear summary (counts, duration, status).

## Security Notes

- Never commit the Linux or Windows binaries to the source repository.
- Release binaries are attached to GitHub Releases, not stored in git history.
- SHA-256 checksums are generated for every release asset.
- The updater verifies checksums before replacing the local binary.
