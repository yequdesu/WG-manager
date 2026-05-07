#!/bin/bash
set -euo pipefail

# WireGuard Management Layer — Admin Offline Peer Creator
# Generate a client config locally, auto-register in management,
# then manually distribute the .conf file to the client.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; }
info() { echo -e "${CYAN}[i]${NC} $*"; }

usage() {
    echo ""
    echo -e "${BOLD}Usage:${NC} $0 --name <peer-name> [options]"
    echo ""
    echo -e "${BOLD}Description:${NC}"
    echo "  Generate a WireGuard client config on the server, auto-register"
    echo "  the peer in the management system, and save the .conf file."
    echo "  Distribute the .conf file to the client manually (email, USB, etc.)."
    echo ""
    echo -e "${BOLD}Options:${NC}"
    echo "  -n, --name <name>       Peer name (required, a-z, A-Z, 0-9, -)"
    echo "  --dns <servers>         Custom DNS servers (default: from config.env)"
    echo "  --keepalive <seconds>   Persistent keepalive interval (default: 25)"
    echo "  -o, --output <path>     Output .conf file path"
    echo "                          (default: scripts/<name>.conf)"
    echo "  -h, --help              Show this help"
    echo ""
    echo -e "${BOLD}Examples:${NC}"
    echo "  $0 --name my-laptop"
    echo "  $0 --name office-pc --dns 8.8.8.8,8.8.4.4"
    echo "  $0 --name phone1 -o /tmp/phone1.conf"
    echo ""
    exit 0
}

# ── Parse arguments ──────────────────────────────────
PEER_NAME=""
PEER_DNS=""
PEER_KEEPALIVE=""
OUTPUT_FILE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--name)
            PEER_NAME="$2"; shift 2 ;;
        --name=*)
            PEER_NAME="${1#*=}"; shift ;;
        --dns)
            PEER_DNS="$2"; shift 2 ;;
        --dns=*)
            PEER_DNS="${1#*=}"; shift ;;
        --keepalive)
            PEER_KEEPALIVE="$2"; shift 2 ;;
        --keepalive=*)
            PEER_KEEPALIVE="${1#*=}"; shift ;;
        -o|--output)
            OUTPUT_FILE="$2"; shift 2 ;;
        --output=*)
            OUTPUT_FILE="${1#*=}"; shift ;;
        -h|--help)
            usage ;;
        *)
            err "Unknown argument: $1"
            usage ;;
    esac
done

if [[ -z "$PEER_NAME" ]]; then
    err "Peer name is required (use --name <name>)"
    usage
fi

# Validate peer name (a-z, A-Z, 0-9, - only, max 64 chars)
if [[ ${#PEER_NAME} -gt 64 ]]; then
    err "Peer name too long (max 64 characters)"
    exit 1
fi
if [[ ! "$PEER_NAME" =~ ^[a-zA-Z0-9-]+$ ]]; then
    err "Peer name contains invalid characters (use a-z, A-Z, 0-9, -)"
    exit 1
fi

# ── Load config ──────────────────────────────────────
if [[ ! -f "$CONFIG_FILE" ]]; then
    err "config.env not found at $CONFIG_FILE"
    err "Run setup-server.sh first."
    exit 1
fi

API_KEY=$(grep MGMT_API_KEY "$CONFIG_FILE" | cut -d= -f2)
MGMT_LISTEN=$(grep MGMT_LISTEN "$CONFIG_FILE" | sed 's/^[^=]*=//' | tr -d ' ')
WG_SUBNET=$(grep WG_SUBNET "$CONFIG_FILE" | cut -d= -f2)
DEFAULT_DNS=$(grep DEFAULT_DNS "$CONFIG_FILE" | cut -d= -f2)
DEFAULT_KEEPALIVE=$(grep PEER_KEEPALIVE "$CONFIG_FILE" | cut -d= -f2)

MGMT_HOST="${MGMT_LISTEN%:*}"
MGMT_PORT="${MGMT_LISTEN##*:}"
[ -z "$MGMT_PORT" ] && MGMT_PORT=58880
if [[ "$MGMT_HOST" == "0.0.0.0" ]]; then
    MGMT_HOST="127.0.0.1"
fi

if [[ -z "$PEER_DNS" ]]; then
    PEER_DNS="$DEFAULT_DNS"
fi
if [[ -z "$PEER_KEEPALIVE" ]]; then
    PEER_KEEPALIVE="${DEFAULT_KEEPALIVE:-25}"
fi

# ── Set output path ──────────────────────────────────
if [[ -z "$OUTPUT_FILE" ]]; then
    OUTPUT_FILE="$SCRIPT_DIR/${PEER_NAME}.conf"
fi

# ── Check daemon ─────────────────────────────────────
if ! systemctl is-active --quiet wg-mgmt 2>/dev/null; then
    err "Management daemon (wg-mgmt) is not running"
    err "Start it: systemctl start wg-mgmt"
    exit 1
fi

# ── Register peer via local API ──────────────────────
info "Registering peer '$PEER_NAME'..."

RESP=$(curl -sSf --connect-timeout 5 --max-time 10 \
    -X POST "http://${MGMT_HOST}:${MGMT_PORT}/api/v1/register" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"hostname\":\"${PEER_NAME}\",\"dns\":\"${PEER_DNS}\"}" 2>&1) || {
    err "Failed to register. Response: $RESP"
    exit 1
}

# ── Parse response ───────────────────────────────────
json_get() {
    local json="$1" key="$2" default="${3:-}"
    if command -v jq &>/dev/null; then
        echo "$json" | jq -r ".$key" 2>/dev/null || echo "$default"
    elif command -v python3 &>/dev/null; then
        echo "$json" | python3 -c "import sys,json; print(json.load(sys.stdin)['$key'])" 2>/dev/null || echo "$default"
    else
        echo "$default"
    fi
}

ERROR=$(json_get "$RESP" "error" "")
if [[ -n "$ERROR" ]]; then
    err "API error: $ERROR"
    exit 1
fi

ADDR=$(json_get "$RESP" "peer.address" "")
KEY=$(json_get "$RESP" "peer.private_key" "")
SPUB=$(json_get "$RESP" "peer.server_public_key" "")
SEP=$(json_get "$RESP" "peer.server_endpoint" "")
DNS=$(json_get "$RESP" "peer.dns" "$PEER_DNS")
KA=$(json_get "$RESP" "peer.keepalive" "$PEER_KEEPALIVE")

if [[ -z "$ADDR" ]] || [[ -z "$KEY" ]]; then
    err "Failed to parse peer config from API response."
    echo "$RESP" | python3 -m json.tool 2>/dev/null || echo "$RESP"
    exit 1
fi

# ── Generate WireGuard config ────────────────────────
cat > "$OUTPUT_FILE" << CONFEOF
[Interface]
Address = $ADDR
PrivateKey = $KEY
DNS = $DNS

[Peer]
PublicKey = $SPUB
Endpoint = $SEP
AllowedIPs = $WG_SUBNET
PersistentKeepalive = $KA
CONFEOF

chmod 600 "$OUTPUT_FILE"

# ── Done ─────────────────────────────────────────────
log "Peer '$PEER_NAME' registered and added to WireGuard"
info "  VPN IP:     ${ADDR%/*}"
info "  DNS:        $DNS"
info "  Endpoint:   $SEP"
info "  Config:     $OUTPUT_FILE"
echo ""
info "Distribute this file to the client:"
echo -e "  ${BOLD}${OUTPUT_FILE}${NC}"
echo ""
echo -e "  ${BOLD}Linux/macOS/WSL client:${NC}"
echo "    sudo cp ${OUTPUT_FILE} /etc/wireguard/wg0.conf && sudo wg-quick up wg0"
echo ""
echo -e "  ${BOLD}Windows client:${NC}"
echo "    WireGuard App → Import Tunnel(s) from file → select the .conf → Activate"
echo ""
echo -e "  ${BOLD}Mobile client:${NC}"
echo "    WireGuard App → Create from file or archive → select the .conf"
echo ""
warn "Keep the .conf file secure — it contains the client's private key."
