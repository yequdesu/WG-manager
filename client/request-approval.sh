#!/bin/bash
set -euo pipefail

# WireGuard Management Layer - Request Approval Client
# For clients without API Key. Submits request and polls for admin approval.
#
# Usage: bash request-approval.sh <SERVER_IP> <MGMT_PORT> [HOSTNAME] [DNS]

SERVER_IP="${1:-}"
MGMT_PORT="${2:-58880}"
CLIENT_NAME="${3:-}"
CLIENT_DNS="${4:-1.1.1.1,8.8.8.8}"
POLL_INTERVAL=10
POLL_TIMEOUT=300  # 5 minutes

# Parse --name override
for i in "$@"; do
    case "$i" in
        --name=*) CLIENT_NAME="${i#*=}" ;;
        --name)   shift; CLIENT_NAME="$1" ;;
    esac
done
# Trim leading args if --name was last arg
if [[ "$CLIENT_NAME" == "$1" ]] && [[ "$1" != "${1#--}" ]]; then
    CLIENT_NAME="${3:-}"
fi

sanitize_name() { echo "$1" | tr -cd '[:alnum:]-_' | head -c 32; }
if [[ -z "$CLIENT_NAME" ]]; then
    if [[ -t 0 ]] && [[ -z "$CLIENT_NAME" ]]; then
        read -r -p "$(echo -e "\033[1mEnter peer name\033[0m [$(hostname)]: ")" CLIENT_NAME
    fi
    CLIENT_NAME="${CLIENT_NAME:-$(hostname 2>/dev/null || echo "client")}"
fi
CLIENT_NAME=$(sanitize_name "$CLIENT_NAME")

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()    { echo -e "${GREEN}[+]${NC} $*"; }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; }
error()  { echo -e "${RED}[x]${NC} $*"; }

# Auto-sudo: re-exec with sudo if not root
if [[ "$(id -u)" -ne 0 ]]; then
    exec sudo bash "$0" "$@"
fi

if [[ -z "$SERVER_IP" ]]; then
    echo "Usage: bash request-approval.sh <SERVER_IP> [PORT] [NAME] [DNS]"
    echo "Example: bash request-approval.sh 1.2.3.4 58880"
    exit 1
fi

detect_os() {
    if [[ "$(uname)" == "Darwin" ]]; then OS="macos"
    elif [[ -f /etc/os-release ]]; then . /etc/os-release; OS="$ID"
    elif command -v apt-get &>/dev/null; then OS="debian"
    elif command -v dnf &>/dev/null; then OS="fedora"
    else error "Cannot detect OS"; exit 1; fi
}

install_wireguard() {
    if command -v wg &>/dev/null; then return 0; fi
    case "$OS" in
        ubuntu|debian) apt-get update -qq && apt-get install -y wireguard wireguard-tools ;;
        fedora|centos|rhel) dnf install -y wireguard-tools 2>/dev/null || (yum install -y epel-release && yum install -y wireguard-tools) ;;
        arch) pacman -Sy --noconfirm wireguard-tools ;;
        macos) brew install wireguard-tools ;;
    esac
}

# ── Phase 1: Submit request ────────────────────────────
echo ""
echo -e "${CYAN}╔════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   WG-Manager — Request Access             ║${NC}"
echo -e "${CYAN}╚════════════════════════════════════════════╝${NC}"
echo ""

detect_os
install_wireguard

log "Submitting access request as '$CLIENT_NAME'..."
RESP=$(curl -sSf -X POST "http://${SERVER_IP}:${MGMT_PORT}/api/v1/request" \
    -H "Content-Type: application/json" \
    -d "{\"hostname\":\"${CLIENT_NAME}\",\"dns\":\"${CLIENT_DNS}\"}" 2>&1) || {
    error "Failed to submit request: $RESP"
    exit 1
}

REQUEST_ID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])" 2>/dev/null || echo "")
if [[ -z "$REQUEST_ID" ]]; then
    echo "$RESP" | python3 -m json.tool 2>/dev/null || echo "$RESP"
    exit 1
fi

log "Request ID: $REQUEST_ID"
log "Server:     ${SERVER_IP}:${MGMT_PORT}"
echo ""
echo -e "${YELLOW}Waiting for admin approval...${NC}"
echo -e "${YELLOW}(An admin must run: curl -X POST http://${SERVER_IP}:${MGMT_PORT}/api/v1/requests/${REQUEST_ID}/approve -H 'Authorization: Bearer <API_KEY>')${NC}"
echo ""

# ── Phase 2: Poll for approval ────────────────────────
ELAPSED=0
APPROVED=false
PEER_CONFIG=""

while [[ $ELAPSED -lt $POLL_TIMEOUT ]]; do
    sleep $POLL_INTERVAL
    ELAPSED=$((ELAPSED + POLL_INTERVAL))

    STATUS_RESP=$(curl -s "http://${SERVER_IP}:${MGMT_PORT}/api/v1/request/${REQUEST_ID}" 2>/dev/null || echo '{"status":"poll_error"}')
    STATUS=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','error'))" 2>/dev/null || echo "error")

    case "$STATUS" in
        pending)
            echo -e "${CYAN}[${ELAPSED}s]${NC} Still waiting..."
            ;;
        approved)
            log "Request approved! Fetching configuration..."
            APPROVED=true
            PEER_ADDR=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['address'])" 2>/dev/null || echo "")
            PEER_KEY=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['private_key'])" 2>/dev/null || echo "")
            SRV_KEY=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_public_key'])" 2>/dev/null || echo "")
            SRV_EP=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['server_endpoint'])" 2>/dev/null || echo "")
            PEER_DNS=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['dns'])" 2>/dev/null || echo "$CLIENT_DNS")
            PEER_KA=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['keepalive'])" 2>/dev/null || echo "25")

            PEER_CONFIG=$(cat <<WGCONF
[Interface]
Address = $PEER_ADDR
PrivateKey = $PEER_KEY
DNS = $PEER_DNS

[Peer]
PublicKey = $SRV_KEY
Endpoint = $SRV_EP
AllowedIPs = 10.0.0.0/24
PersistentKeepalive = $PEER_KA
WGCONF
)
            break
            ;;
        rejected)
            error "Request was rejected by the admin."
            exit 1
            ;;
        expired)
            error "Request has expired. Please submit a new request."
            exit 1
            ;;
        not_found)
            error "Request not found. It may have been processed by an admin."
            # Try to fetch config anyway (peer may have been approved and status endpoint removed)
            CONFIG_RESP=$(curl -s "http://${SERVER_IP}:${MGMT_PORT}/api/v1/windows-config?name=${CLIENT_NAME}" 2>/dev/null || echo "")
            if echo "$CONFIG_RESP" | grep -q "PrivateKey"; then
                PEER_CONFIG="$CONFIG_RESP"
                APPROVED=true
                log "Config found! Your request was approved."
                break
            fi
            exit 1
            ;;
        error|poll_error)
            echo -e "${YELLOW}[${ELAPSED}s]${NC} Connection issue, retrying..."
            ;;
        *)
            echo -e "${YELLOW}[${ELAPSED}s]${NC} Unknown status: $STATUS"
            echo "$STATUS_RESP" | python3 -m json.tool 2>/dev/null || true
            ;;
    esac
done

if ! $APPROVED || [[ -z "$PEER_CONFIG" ]]; then
    error "Could not fetch configuration. Please contact your admin."
    exit 1
fi

# ── Phase 3: Write config and connect ──────────────────
WG_CONF="/etc/wireguard/wg0.conf"
mkdir -p /etc/wireguard
echo "$PEER_CONFIG" > "$WG_CONF"
chmod 600 "$WG_CONF"
log "Config written to $WG_CONF"

if command -v systemctl &>/dev/null; then
    systemctl enable "wg-quick@wg0" --quiet 2>/dev/null || true
    systemctl restart "wg-quick@wg0"
elif [[ "$OS" == "macos" ]]; then
    wg-quick up wg0 &
else
    wg-quick up wg0 &
fi

sleep 2
log "Checking WireGuard status..."
wg show wg0 2>/dev/null || true

echo ""
echo -e "${GREEN}${BOLD}Connected!${NC}"
echo -e "  $(grep Address "$WG_CONF" || true)"
