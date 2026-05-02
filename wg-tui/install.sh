#!/bin/bash
set -euo pipefail

# WG-TUI Installer — Ratatui Dashboard for WG-Manager
# Usage: bash install.sh

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
RED='\033[0;31m'
NC='\033[0m'

log()    { echo -e "${GREEN}[+]${NC} $*"; }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; }
info()   { echo -e "${CYAN}[i]${NC} $*"; }
err()    { echo -e "${RED}[x]${NC} $*"; }

echo ""
echo -e "${BOLD}${CYAN}  WG-TUI Installer${NC}"
echo -e "  Ratatui Dashboard for WG-Manager"
echo ""

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# 1. Check Rust toolchain
if ! command -v cargo &>/dev/null; then
    warn "Rust toolchain not found."
    read -p "$(echo -e "${BOLD}  Install Rust via rustup? [Y/n]: ${NC}")" ans
    if [[ "$ans" =~ ^[Nn] ]]; then
        err "Rust is required. Install manually: https://rustup.rs"
        exit 1
    fi
    log "Installing Rust..."
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
    source "$HOME/.cargo/env"
    log "Rust installed: $(rustc --version)"
else
    log "Rust found: $(rustc --version)"
fi

# 2. Build
cd "$SCRIPT_DIR"
log "Building wg-tui (release, optimized)..."
cargo build --release 2>&1 | tail -5

BIN="target/release/wg-tui"
if [[ ! -f "$BIN" ]]; then
    err "Build failed — binary not found at $BIN"
    exit 1
fi

# 3. Install
DEST="${HOME}/.local/bin/wg-tui"
if [[ -w /usr/local/bin ]]; then
    DEST="/usr/local/bin/wg-tui"
fi
mkdir -p "$(dirname "$DEST")"
cp "$BIN" "$DEST"
chmod +x "$DEST"

# 4. Verify
echo ""
log "Installed to: ${BOLD}$DEST${NC}"
echo ""
info "Usage:"
echo "  ${BOLD}wg-tui${NC}"
echo ""
info "Make sure the WG-Manager daemon is running on localhost."
echo "  The TUI reads config from ./config.env or ~/WG-manager/config.env"
echo ""

# Check if in PATH
if command -v wg-tui &>/dev/null; then
    log "Ready: wg-tui is in your PATH"
else
    warn "Add ${DEST%/*} to your PATH:"
    echo "  export PATH=\"${DEST%/*}:\$PATH\""
fi
