#!/bin/bash
set -euo pipefail
# WireGuard Management Layer — Direct Join Script
# Served by /connect?mode=direct

SERVER_IP="__SERVER_PUBLIC_IP__"
MGMT_PORT="__MGMT_PORT__"
API_KEY="__API_KEY__"
DEFAULT_DNS="__DEFAULT_DNS__"
PEER_NAME="__PEER_NAME__"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
if [[ -f "$SCRIPT_DIR/lib/os-detect.sh" ]]; then
    source "$SCRIPT_DIR/lib/os-detect.sh"
elif [[ -f "/tmp/wg-client-lib.sh" ]]; then
    source "/tmp/wg-client-lib.sh"
fi

auto_sudo "$@"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; }

# ── Phase 1: Setup ─────────────────────────────────
detect_os
ensure_wireguard

# Parse --name arg
for arg in "$@"; do case "$arg" in
    --name=*) PEER_NAME="${arg#*=}" ;;
    --name)   shift; PEER_NAME="$1" ;;
esac; done
[[ -z "$PEER_NAME" ]] && PEER_NAME="$(hostname 2>/dev/null || echo "client")"

# ── Phase 2: Register ──────────────────────────────
log "Registering as '$PEER_NAME'..."
RESP=$(curl -sSf -X POST "http://${SERVER_IP}:${MGMT_PORT}/api/v1/register" \
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
AllowedIPs = 10.0.0.0/24
PersistentKeepalive = $KA
EOF
chmod 600 "$WG_CONF"
log "Config written to $WG_CONF"

wg_service wg0
sleep 1
wg show wg0 2>/dev/null || true
log "Connected. Your VPN IP: $ADDR"
