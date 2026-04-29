#!/bin/bash
set -euo pipefail
# WireGuard Management Layer — Approval Join Script
# Served by /connect (default)

SERVER_IP="__SERVER_IP__"
MGMT_PORT="__MGMT_PORT__"
PEER_NAME="__PEER_NAME__"
DEFAULT_DNS="1.1.1.1,8.8.8.8"
POLL_INTERVAL=10
POLL_TIMEOUT=300

# ── Source platform lib ────────────────────────────
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

# ── Parse args ─────────────────────────────────────
for arg in "$@"; do case "$arg" in
    --name=*) PEER_NAME="${arg#*=}" ;;
    --name)   shift; PEER_NAME="$1" ;;
esac; done

[[ -n "$1" ]] && [[ "$1" != "${1#--}" ]] && SERVER_IP="$1"
[[ -n "${2:-}" ]] && [[ "$2" != "${2#--}" ]] && MGMT_PORT="$2"
[[ -n "${3:-}" ]] && [[ "$3" != "${3#--}" ]] && PEER_NAME="$3"
[[ -z "$PEER_NAME" ]] && [[ -t 0 ]] && read -r -p "$(echo -e "${BOLD}Enter peer name${NC}: ")" PEER_NAME
[[ -z "$PEER_NAME" ]] && PEER_NAME="$(hostname 2>/dev/null || echo "client")"

# ── Phase 1: Setup ─────────────────────────────────
detect_os
ensure_wireguard

# ── Phase 2: Submit request ────────────────────────
log "Submitting access request as '$PEER_NAME'..."
RESP=$(curl -sSf -X POST "http://${SERVER_IP}:${MGMT_PORT}/api/v1/request" \
    -H "Content-Type: application/json" \
    -d "{\"hostname\":\"${PEER_NAME}\",\"dns\":\"${DEFAULT_DNS}\"}" 2>&1) || {
    err "Failed to submit: $RESP"; exit 1
}
REQ_ID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])" 2>/dev/null || echo "")
[[ -z "$REQ_ID" ]] && { echo "$RESP" | python3 -m json.tool 2>/dev/null || echo "$RESP"; exit 1; }
log "Request ID: $REQ_ID"
warn "Waiting for admin approval..."

# ── Phase 3: Poll ──────────────────────────────────
ELAPSED=0; APPROVED=false; PEER_CONFIG=""
while [[ $ELAPSED -lt $POLL_TIMEOUT ]]; do
    sleep $POLL_INTERVAL; ELAPSED=$((ELAPSED + POLL_INTERVAL))
    SR=$(curl -s "http://${SERVER_IP}:${MGMT_PORT}/api/v1/request/${REQ_ID}" 2>/dev/null || echo '{"status":"error"}')
    ST=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','error'))" 2>/dev/null || echo "error")
    case "$ST" in
        pending) echo -e "${CYAN}[${ELAPSED}s]${NC} waiting..." ;;
        approved)
            log "Approved! Configuring..."
            APPROVED=true
            ADDR=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['address'])" 2>/dev/null || echo "")
            KEY=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['private_key'])" 2>/dev/null || echo "")
            SPUB=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_public_key'])" 2>/dev/null || echo "")
            SEP=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_endpoint'])" 2>/dev/null || echo "")
            DNS=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['dns'])" 2>/dev/null || echo "$DEFAULT_DNS")
            KA=$(echo "$SR" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['keepalive'])" 2>/dev/null || echo "25")
            PEER_CONFIG=$(printf "[Interface]\nAddress = %s\nPrivateKey = %s\nDNS = %s\n\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = 10.0.0.0/24\nPersistentKeepalive = %s\n" "$ADDR" "$KEY" "$DNS" "$SPUB" "$SEP" "$KA")
            break
            ;;
        rejected)  err "Request was REJECTED."; exit 1 ;;
        expired)   err "Request EXPIRED. Submit again."; exit 1 ;;
        error|poll_error) echo -e "${YELLOW}[${ELAPSED}s]${NC} retrying..." ;;
        *) echo -e "${YELLOW}[${ELAPSED}s]${NC} status: $ST" ;;
    esac
done

if ! $APPROVED; then
    err "Timed out after ${POLL_TIMEOUT}s."; exit 1
fi
[[ -z "$PEER_CONFIG" ]] && { err "Could not get config."; exit 1; }

# ── Phase 4: Write config and connect ──────────────
WG_CONF="/etc/wireguard/wg0.conf"
mkdir -p /etc/wireguard
echo "$PEER_CONFIG" > "$WG_CONF"
chmod 600 "$WG_CONF"
log "Config written to $WG_CONF"

wg_service wg0
sleep 1
wg show wg0 2>/dev/null || true
log "Connected."
