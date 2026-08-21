#!/usr/bin/env bash
# takeOutBack launcher for Linux.
# Resolves the project root from the script's location, applies any staged
# update and invokes the local portable binary. No installation, no PATH,
# no admin rights required.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UPDATE_DIR="${SCRIPT_DIR}/TakeOutBack/.update"
BIN="${SCRIPT_DIR}/TakeOutBack/tools/linux/takeoutback"

# Copy src to dst atomically so that a running executable can be replaced.
atomic_copy() {
    local src="$1" dst="$2"
    local tmp="${dst}.tmp.$$"
    cp -f "${src}" "${tmp}"
    mv -f "${tmp}" "${dst}"
}

# Apply a staged update created by 'takeoutback update'. The .update directory
# contains a TakeOutBack/ subtree plus root-level files.
if [ -f "${UPDATE_DIR}/pending" ]; then
    echo "Applying staged update..."
    if [ -d "${UPDATE_DIR}/TakeOutBack" ]; then
        while IFS= read -r -d '' src; do
            rel="${src#${UPDATE_DIR}/TakeOutBack/}"
            dst="${SCRIPT_DIR}/TakeOutBack/${rel}"
            if [ -d "${src}" ]; then
                mkdir -p "${dst}"
            else
                atomic_copy "${src}" "${dst}"
            fi
        done < <(find "${UPDATE_DIR}/TakeOutBack" -print0)
    fi
    for f in takeOutBack.sh takeOutBack.bat README.md CHANGELOG.md; do
        if [ -f "${UPDATE_DIR}/${f}" ]; then
            atomic_copy "${UPDATE_DIR}/${f}" "${SCRIPT_DIR}/${f}"
        fi
    done
    rm -rf "${UPDATE_DIR}"
    echo "Update applied."
fi

# Legacy .next file support: remove after one migration cycle.
NEXT="${BIN}.next"
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
