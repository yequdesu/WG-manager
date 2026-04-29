#!/bin/bash
set -euo pipefail
# WireGuard Management Layer — Direct Join Script
# Served by /connect?mode=direct

SERVER_IP="__SERVER_PUBLIC_IP__"
MGMT_PORT="__MGMT_PORT__"
API_KEY="__API_KEY__"
DEFAULT_DNS="__DEFAULT_DNS__"
PEER_NAME="__PEER_NAME__"

# ── inline helpers (piped script has no file to source) ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; }

auto_sudo() {
    if [[ "$(id -u)" -ne 0 ]]; then
        echo "[x] Run as root: curl ... | sudo bash"
        exit 1
    fi
}
auto_sudo "$@"

detect_os() {
    if [[ "$(uname)" == "Darwin" ]]; then OS="macos"; PKG="brew"; return 0; fi
    [[ -f /etc/os-release ]] && . /etc/os-release && OS="$ID" || { OS="unknown"; return 1; }
    case "$OS" in
        ubuntu|debian)  PKG="apt" ;;
        fedora|centos|rhel|rocky|alma) PKG="dnf" ;;
        arch)           PKG="pacman" ;;
        alpine)         PKG="apk" ;;
        *)              PKG="unknown" ;;
    esac
}

ensure_wireguard() {
    if command -v wg &>/dev/null; then log "WireGuard ready ($(wg --version 2>&1|head -1))"; return 0; fi
    warn "WireGuard not installed."
    if [[ -t 0 ]]; then read -r -p "    Install now? [Y/n]: " c; [[ "$c" =~ ^[Nn] ]] && { echo "Install manually and re-run."; exit 1; }; fi
    case "$PKG" in
        apk) apk add wireguard-tools ;; apt) apt-get update -qq; apt-get install -y wireguard wireguard-tools ;;
        dnf) dnf install -y wireguard-tools ;; yum) yum install -y epel-release; yum install -y wireguard-tools ;;
        pacman) pacman -Sy --noconfirm wireguard-tools ;; brew) brew install wireguard-tools ;;
        *) echo "Unknown pkg manager. Install wireguard-tools manually."; exit 1 ;;
    esac
    log "WireGuard installed"
}

wg_service() {
    local iface="${1:-wg0}"
    if command -v systemctl &>/dev/null; then systemctl enable "wg-quick@$iface" --quiet 2>/dev/null; systemctl restart "wg-quick@$iface"
    elif command -v rc-service &>/dev/null; then rc-update add "wg-quick@$iface" 2>/dev/null; rc-service "wg-quick@$iface" restart
    else wg-quick up "$iface" & fi
}

# ── main ──

# ── Phase 1: Setup ─────────────────────────────────
detect_os
ensure_wireguard

# Parse --name arg
for arg in "$@"; do case "$arg" in
    --name=*) PEER_NAME="${arg#*=}" ;;
    --name)   shift; PEER_NAME="${1:-}" ;;
esac; done
if [[ -z "$PEER_NAME" ]] && [[ -t 0 ]]; then
    read -r -p "$(echo -e "${BOLD}Enter peer name${NC}: ")" PEER_NAME
fi
[[ -z "$PEER_NAME" ]] && PEER_NAME="$(hostname 2>/dev/null || echo "client")"

# ── Phase 2: Register ──────────────────────────────
log "Registering as '$PEER_NAME'..."
RESP=$(curl --connect-timeout 5 --max-time 10 -sSf -X POST "http://${SERVER_IP}:${MGMT_PORT}/api/v1/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"hostname\":\"${PEER_NAME}\",\"dns\":\"${DEFAULT_DNS}\"}" 2>&1) || {
    err "Failed to register. Response: $RESP"; exit 1
}

ADDR=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['address'])" 2>/dev/null || echo "")
KEY=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['private_key'])" 2>/dev/null || echo "")
SPUB=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_public_key'])" 2>/dev/null || echo "")
SEP=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_endpoint'])" 2>/dev/null || echo "")
DNS=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['dns'])" 2>/dev/null || echo "$DEFAULT_DNS")
KA=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['keepalive'])" 2>/dev/null || echo "25")
log "Registered: $PEER_NAME ($ADDR)"

# ── Phase 3: Write config and start ─────────────────
WG_CONF="/etc/wireguard/wg0.conf"
mkdir -p /etc/wireguard
cat > "$WG_CONF" << EOF
[Interface]
Address = $ADDR
PrivateKey = $KEY
DNS = $DNS

[Peer]
PublicKey = $SPUB
Endpoint = $SEP
AllowedIPs = __WG_ALLOWED_IPS__
PersistentKeepalive = $KA
EOF
chmod 600 "$WG_CONF"
log "Config written to $WG_CONF"

wg_service wg0
sleep 1
wg show wg0 2>/dev/null || true
log "Connected. Your VPN IP: $ADDR"
