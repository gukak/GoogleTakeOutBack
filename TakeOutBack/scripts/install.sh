#!/usr/bin/env bash
# TakeOutBack installer for Linux/macOS.
# Usage: curl -fsSL <URL>/install.sh | bash
# Set TAKEOUTBACK_VERSION to override the release tag.
set -euo pipefail

OWNER_REPO="gukak/GoogleTakeOutBack"
VERSION="${TAKEOUTBACK_VERSION:-v0.3.2}"
FETCH_BOTH="${TAKEOUTBACK_FETCH_BOTH:-1}"
FORCE=0

while [ $# -gt 0 ]; do
    case "$1" in
        --force) FORCE=1; shift ;;
        --version) VERSION="$2"; shift 2 ;;
        --no-both) FETCH_BOTH=0; shift ;;
        -h|--help)
            echo "Usage: curl -fsSL <url>/install.sh | bash [ -s -- --force ]"
            exit 0
            ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
    linux) ;;
    *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

ROOT=$(pwd)

if [ "$FORCE" -eq 0 ]; then
    entries=$(ls -A 2>/dev/null || true)
    if [ -n "$entries" ] && [ ! -f "$ROOT/.takeoutback-root" ]; then
        echo "Directory is not empty. Run with --force or use an empty directory." >&2
        exit 1
    fi
fi

mkdir -p "$ROOT/Incoming" "$ROOT/Archive"
mkdir -p "$ROOT/TakeOutBack/app"
mkdir -p "$ROOT/TakeOutBack/tools/linux"
mkdir -p "$ROOT/TakeOutBack/tools/windows"
mkdir -p "$ROOT/TakeOutBack/temp"
mkdir -p "$ROOT/TakeOutBack/logs"
mkdir -p "$ROOT/TakeOutBack/config"
mkdir -p "$ROOT/TakeOutBack/scripts"
mkdir -p "$ROOT/TakeOutBack/docs"
echo "TakeOutBack project root" > "$ROOT/.takeoutback-root"

BASE="https://github.com/$OWNER_REPO/releases/download/$VERSION"

download() {
    local url=$1 out=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --max-time 120 "$url" -o "$out"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$out" "$url"
    else
        echo "curl or wget is required" >&2
        exit 1
    fi
}

verify_checksum() {
    local file=$1 sumfile=$2
    local expected got
    expected=$(awk '{print $1}' "$sumfile")
    if command -v sha256sum >/dev/null 2>&1; then
        got=$(sha256sum "$file" | awk '{print $1}')
    else
        got=$(shasum -a 256 "$file" | awk '{print $1}')
    fi
    if [ "$got" != "$expected" ]; then
        echo "Checksum mismatch for $file" >&2
        exit 1
    fi
}

fetch_binary() {
    local name=$1 out=$2
    local tmp="$out.tmp"
    download "$BASE/$name" "$tmp"
    download "$BASE/$name.sha256" "$tmp.sha256"
    verify_checksum "$tmp" "$tmp.sha256"
    rm -f "$tmp.sha256"
    chmod +x "$tmp"
    mv "$tmp" "$out"
}

fetch_binary "takeoutback-${OS}-amd64" "$ROOT/TakeOutBack/tools/linux/takeoutback"
if [ "$FETCH_BOTH" -eq 1 ]; then
    fetch_binary "takeoutback-windows-amd64.exe" "$ROOT/TakeOutBack/tools/windows/takeoutback.exe"
fi

download "$BASE/TakeOutBack.sh" "$ROOT/TakeOutBack.sh"
chmod +x "$ROOT/TakeOutBack.sh"
download "$BASE/TakeOutBack.bat" "$ROOT/TakeOutBack.bat"
download "$BASE/settings.json" "$ROOT/TakeOutBack/config/settings.json"
download "$BASE/policy.json" "$ROOT/TakeOutBack/config/policy.json"
download "$BASE/VERSION" "$ROOT/TakeOutBack/config/VERSION"
download "$BASE/README.md" "$ROOT/TakeOutBack/docs/README.md"

echo "TakeOutBack $VERSION installed in $ROOT"
echo "Place Google Takeout ZIP files in $ROOT/Incoming and run $ROOT/TakeOutBack.sh"
