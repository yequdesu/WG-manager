#!/bin/bash
set -euo pipefail

# Cross-compile wg-tui for Linux from any platform
# Uses musl target for static binary (no glibc dependency)
#
# Prerequisites (one-time):
#   rustup target add x86_64-unknown-linux-musl
#
# Usage:
#   bash build-linux.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

TARGET="x86_64-unknown-linux-musl"
BIN="target/${TARGET}/release/wg-tui"
OUT="${SCRIPT_DIR}/wg-tui-ratatui-linux"

echo "=== wg-tui cross-compiler ==="
echo "Target: ${TARGET}"
echo ""

# 1. Ensure target is installed
if ! rustup target list --installed 2>/dev/null | grep -q "$TARGET"; then
    echo "[+] Adding target: $TARGET"
    rustup target add "$TARGET"
fi

# 2. Build (musl + rustls = pure Rust, no C linker needed)
echo "[+] Building release for $TARGET ..."

# Use zigbuild if available (handles edge cases with C deps)
if command -v cargo-zigbuild &>/dev/null 2>&1; then
    cargo zigbuild --release --target "$TARGET"
else
    cargo build --release --target "$TARGET"
fi

# 3. Copy binary
if [[ -f "$BIN" ]]; then
    cp "$BIN" "$OUT"
    chmod +x "$OUT"
    echo ""
    echo "[+] Done: $OUT"
    ls -lh "$OUT"
    echo ""
    echo "Deploy to server:"
    echo "  scp $OUT user@server:~/.local/bin/wg-tui"
else
    echo "[x] Build failed — binary not found"
    exit 1
fi
