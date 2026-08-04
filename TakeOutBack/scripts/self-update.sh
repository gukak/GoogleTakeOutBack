#!/usr/bin/env bash
# Self-update wrapper: invokes the embedded updater shipped with the binary.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/TakeOutBack.sh" update "$@"
