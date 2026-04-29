#!/bin/bash
set -euo pipefail

# Health check script for WireGuard Management Layer
# Can be run manually or added to cron

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

HAS_ERROR=0

echo "=== WireGuard Health Check ==="
echo "Time: $(date)"
echo ""

# Check systemd services
echo "--- Services ---"
for svc in wg-quick@wg0 wg-mgmt; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        pass "$svc is running"
    else
        fail "$svc is NOT running"
        HAS_ERROR=1
    fi
done

echo ""

# Check management API health
echo "--- Management API ---"
MGMT_LISTEN=$(grep MGMT_LISTEN "$CONFIG_FILE" 2>/dev/null | sed 's/^[^=]*=//' | tr -d ' ' || echo "127.0.0.1:58880")
MGMT_HOST="${MGMT_LISTEN%:*}"
MGMT_PORT="${MGMT_LISTEN##*:}"
[ -z "$MGMT_PORT" ] && MGMT_PORT=58880
if [[ "$MGMT_HOST" == "0.0.0.0" ]]; then
    MGMT_HOST="127.0.0.1"
fi

HEALTH=$(curl -s -o /dev/null -w "%{http_code}" "http://${MGMT_HOST}:${MGMT_PORT}/api/v1/health" 2>/dev/null || echo "000")
if [[ "$HEALTH" == "200" ]]; then
    pass "Management API health check OK"
else
    fail "Management API health check FAILED (HTTP $HEALTH)"
    HAS_ERROR=1
fi

echo ""

# Check WireGuard interface and peers
echo "--- WireGuard Status ---"
if command -v wg &>/dev/null; then
    WG_OUTPUT=$(wg show wg0 2>/dev/null || true)
    if [[ -z "$WG_OUTPUT" ]]; then
        fail "WireGuard interface wg0 not found"
        HAS_ERROR=1
    else
        pass "WireGuard interface wg0 is active"

        PEER_LINES=$(echo "$WG_OUTPUT" | grep -c "peer:" || true)
        echo "  Peers configured: $PEER_LINES"

        if [[ "$PEER_LINES" -gt 0 ]]; then
            NOW=$(date +%s)
            while IFS= read -r line; do
                if [[ "$line" =~ latest\ handshake:\ (.+) ]]; then
                    echo "  Handshake: ${BASH_REMATCH[1]}"
                fi
            done <<< "$WG_OUTPUT"
        fi

        echo ""
        echo "  Transfer:"
        wg show wg0 transfer 2>/dev/null || true
    fi
else
    warn "wg command not available (expected on non-server machines)"
fi

echo ""

# Check port accessibility
echo "--- Ports ---"
WG_PORT=$(grep WG_PORT "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "51820")
if ss -uln 2>/dev/null | grep -q ":$WG_PORT " || netstat -uln 2>/dev/null | grep -q ":$WG_PORT "; then
    pass "UDP port $WG_PORT (WireGuard) is listening"
else
    warn "UDP port $WG_PORT (WireGuard) may not be listening"
fi

if ss -tln 2>/dev/null | grep -q ":$MGMT_PORT " || netstat -tln 2>/dev/null | grep -q ":$MGMT_PORT "; then
    pass "TCP port $MGMT_PORT (Management) is listening"
else
    warn "TCP port $MGMT_PORT (Management) may not be listening"
fi

echo ""

if [[ "$HAS_ERROR" -eq 0 ]]; then
    echo -e "${GREEN}=== All checks passed ===${NC}"
else
    echo -e "${RED}=== Some checks FAILED ===${NC}"
fi

exit $HAS_ERROR
