#!/usr/bin/env bash
# takeOutBack launcher for Linux.
# Resolves the project root from the script's location and invokes the local
# portable binary. No installation, no PATH, no admin rights required.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${SCRIPT_DIR}/TakeOutBack/tools/linux/takeoutback"
NEXT="${BIN}.next"

# If a staged update exists, replace the current binary before launching.
if [ -f "${NEXT}" ]; then
    mv -f "${BIN}" "${BIN}.old" 2>/dev/null || true
    mv -f "${NEXT}" "${BIN}" 2>/dev/null || true
    rm -f "${BIN}.old" 2>/dev/null || true
fi

if [ ! -f "${BIN}" ]; then
    echo "takeoutback: binary not found: ${BIN}" >&2
    exit 1
fi

# Ensure the binary is executable (permissions may be lost when copying from
# Windows/exFAT drives).
if [ ! -x "${BIN}" ]; then
    chmod +x "${BIN}" || {
        echo "takeoutback: cannot make binary executable: ${BIN}" >&2
        exit 1
    }
fi

exec "${BIN}" --root "${SCRIPT_DIR}" "$@"
