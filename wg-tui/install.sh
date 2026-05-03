#!/bin/bash
set -euo pipefail

# WG-TUI Installer — Ratatui Dashboard for WG-Manager
# Usage: bash install.sh [--mirror cn|ustc|tuna]

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

# ── Parse args ──────────────────────────────────────
USE_MIRROR=""
for arg in "$@"; do
    case "$arg" in
        --mirror=*) USE_MIRROR="${arg#*=}" ;;
        --mirror)   USE_MIRROR="cn" ;;
        --cn)       USE_MIRROR="cn" ;;
        --ustc)     USE_MIRROR="ustc" ;;
        --tuna)     USE_MIRROR="tuna" ;;
    esac
done

# ── Mirror helpers ──────────────────────────────────
setup_ustc_mirror() {
    export RUSTUP_DIST_SERVER="https://mirrors.ustc.edu.cn/rust-static"
    export RUSTUP_UPDATE_ROOT="https://mirrors.ustc.edu.cn/rust-static/rustup"
    local cargo_config="${HOME}/.cargo/config.toml"
    mkdir -p "$(dirname "$cargo_config")"
    cat > "$cargo_config" << 'CARGO_CONF'
[source.crates-io]
replace-with = 'ustc'

[source.ustc]
registry = "sparse+https://mirrors.ustc.edu.cn/crates.io-index/"
CARGO_CONF
    log "Using USTC mirrors (mirrors.ustc.edu.cn)"
}

setup_tuna_mirror() {
    export RUSTUP_DIST_SERVER="https://mirrors.tuna.tsinghua.edu.cn/rustup"
    export RUSTUP_UPDATE_ROOT="https://mirrors.tuna.tsinghua.edu.cn/rustup/rustup"
    local cargo_config="${HOME}/.cargo/config.toml"
    mkdir -p "$(dirname "$cargo_config")"
    cat > "$cargo_config" << 'CARGO_CONF'
[source.crates-io]
replace-with = 'tuna'

[source.tuna]
registry = "sparse+https://mirrors.tuna.tsinghua.edu.cn/crates.io-index/"
CARGO_CONF
    log "Using Tsinghua TUNA mirrors (mirrors.tuna.tsinghua.edu.cn)"
}

# ── 1. Mirror prompt ────────────────────────────────
if [[ -z "$USE_MIRROR" ]]; then
    read -p "$(echo -e "${BOLD}  Use China mirror for faster downloads? [y/N/cn=tuna/ustc]: ${NC}")" ans
    case "$ans" in
        [Yy]|yes|cn) USE_MIRROR="cn" ;;
        ustc)        USE_MIRROR="ustc" ;;
        tuna)        USE_MIRROR="tuna" ;;
    esac
fi

case "$USE_MIRROR" in
    cn|ustc) setup_ustc_mirror ;;
    tuna)    setup_tuna_mirror ;;
esac

# ── 2. Detect real user (sudo preserves wrong HOME) ──
REAL_USER="${SUDO_USER:-$USER}"
REAL_HOME="$(eval echo ~"$REAL_USER")"

ensure_cargo_path() {
    if [[ -f "$REAL_HOME/.cargo/bin/cargo" ]]; then
        export PATH="$REAL_HOME/.cargo/bin:$PATH"
        export HOME="$REAL_HOME"
        # shellcheck disable=SC1090
        source "$REAL_HOME/.cargo/env" 2>/dev/null || true
    fi
}

ensure_cargo_path

# ── 3. Check Rust toolchain ──────────────────────────
_rust_ok=false

if command -v rustc &>/dev/null && rustc --version &>/dev/null 2>&1; then
    log "Rust found: $(rustc --version)"
    _rust_ok=true
fi

if command -v rustup &>/dev/null && ! $_rust_ok; then
    log "rustup found but no toolchain installed. Installing stable..."
    if rustup default stable; then
        log "Rust installed: $(rustc --version)"
        _rust_ok=true
    else
        warn "rustup toolchain install failed."
    fi
fi

if ! $_rust_ok; then
    warn "Rust toolchain not found."
    read -p "$(echo -e "${BOLD}  Install Rust via rustup? [Y/n]: ${NC}")" ans
    if [[ "$ans" =~ ^[Nn] ]]; then
        err "Rust is required. Install manually: https://rustup.rs"
        exit 1
    fi
    log "Installing Rust..."
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable
    source "$HOME/.cargo/env"
    log "Rust installed: $(rustc --version)"
fi

# Re-ensure cargo path after potential new install
ensure_cargo_path

# ── 3. Build ─────────────────────────────────────────
cd "$SCRIPT_DIR"
log "Building wg-tui (release, optimized)..."
export CARGO_NET_GIT_FETCH_WITH_CLI=true
cargo build --release

BIN="target/release/wg-tui"
if [[ ! -f "$BIN" ]]; then
    err "Build failed — binary not found at $BIN"
    exit 1
fi

# ── 4. Install enhanced TUI ────────────────────────
DEST="/usr/local/bin/wg-tui-ratatui"
if [[ ! -w /usr/local/bin ]]; then
    DEST="${HOME}/.local/bin/wg-tui-ratatui"
fi
mkdir -p "$(dirname "$DEST")"
cp "$BIN" "$DEST"
chmod +x "$DEST"

# ── 4b. Install launcher wrapper ────────────────────
WRAPPER="/usr/local/bin/wg-tui"
if [[ ! -w /usr/local/bin ]]; then
    WRAPPER="${HOME}/.local/bin/wg-tui"
fi
cat > "$WRAPPER" << 'WRAPPER_EOF'
#!/bin/bash
if [ "${1:-}" = "--legacy" ]; then
    shift
    exec wg-tui-legacy "$@"
elif command -v wg-tui-ratatui &>/dev/null; then
    exec wg-tui-ratatui "$@"
else
    exec wg-tui-legacy "$@"
fi
WRAPPER_EOF
chmod +x "$WRAPPER"

# ── 5. Verify ───────────────────────────────────────
echo ""
log "Installed to: ${BOLD}$DEST${NC}"
log "Launcher: ${BOLD}$WRAPPER${NC}"
echo ""
info "Usage:"
echo "  ${BOLD}wg-tui${NC}              Enhanced Ratatui TUI"
echo "  ${BOLD}wg-tui --legacy${NC}     Original Go TUI"
echo ""
info "Make sure the WG-Manager daemon is running on localhost."
