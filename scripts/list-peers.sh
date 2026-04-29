#!/bin/bash
set -euo pipefail

# Auto-sudo if not root
if [[ "$(id -u)" -ne 0 ]]; then
    exec sudo bash "$0" "$@"
fi

# List all WireGuard peers via management API
# Run on the server (local) only

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Error: config.env not found at $CONFIG_FILE"
    echo "Run setup-server.sh first."
    exit 1
fi

API_KEY=$(grep MGMT_API_KEY "$CONFIG_FILE" | cut -d= -f2)
MGMT_LISTEN=$(grep MGMT_LISTEN "$CONFIG_FILE" | sed 's/^[^=]*=//' | tr -d ' ')

MGMT_HOST="${MGMT_LISTEN%:*}"
MGMT_PORT="${MGMT_LISTEN##*:}"
[ -z "$MGMT_PORT" ] && MGMT_PORT=58880

if [[ "$MGMT_HOST" == "0.0.0.0" ]]; then
    MGMT_HOST="127.0.0.1"
fi

echo "=== WireGuard Peers ==="
echo ""

RESP=$(curl -s "http://${MGMT_HOST}:${MGMT_PORT}/api/v1/peers" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || true)

if [[ -z "$RESP" ]]; then
    echo "Error: Cannot reach management API at ${MGMT_HOST}:${MGMT_PORT}"
    echo "Is the daemon running? (systemctl status wg-mgmt)"
    exit 1
fi

if command -v python3 &>/dev/null; then
    echo "$RESP" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(f\"Server Endpoint: {data.get('server_endpoint', 'N/A')}\")
print(f\"Total Peers:    {data.get('peer_count', 0)}\")
print()
for p in data.get('peers', []):
    online = 'ONLINE' if p.get('online') else 'OFFLINE'
    print(f\"  {p['name']:20s}  {p['address']:15s}  {online:8s}\")
    if p.get('endpoint'):
        print(f\"    Endpoint:    {p['endpoint']}\")
    if p.get('latest_handshake') and p.get('latest_handshake') != '0':
        print(f\"    Handshake:   {p['latest_handshake']}s ago\")
    print()
"
else
    echo "$RESP"
fi
