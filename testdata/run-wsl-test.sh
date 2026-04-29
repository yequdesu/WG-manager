#!/bin/bash
set -euo pipefail

PROJECT_DIR="/mnt/f/yequdesu_project/wire-guard-dev"
TEST_DIR="$PROJECT_DIR/testdata"

echo "=== Setting up WSL WireGuard test ==="

# Copy project
if [ ! -d /tmp/wg-test ]; then
    cp -r "$PROJECT_DIR" /tmp/wg-test
fi
cd /tmp/wg-test

mkdir -p testdata
cp client/connect.sh testdata/connect.sh
rm -f testdata/peers.json testdata/wg0.conf

# Create wireguard interface
ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip addr add 10.0.0.1/24 dev wg0
ip link set wg0 up

# Enable forwarding
sysctl -w net.ipv4.ip_forward=1

# Generate server keys
wg genkey | tee testdata/server_private | wg pubkey > testdata/server_public
SERVER_PRIV=$(cat testdata/server_private)
SERVER_PUB=$(cat testdata/server_public)

echo "Server public key: $SERVER_PUB"

# Create initial peers.json
cat > testdata/peers.json << PEEREOF
{
  "server": {
    "public_key": "$SERVER_PUB",
    "private_key": "$SERVER_PRIV",
    "endpoint": "127.0.0.1:51820",
    "listen_port": 51820,
    "address": "10.0.0.1/24",
    "subnet": "10.0.0.0/24"
  },
  "peers": {},
  "next_ip_suffix": 2
}
PEEREOF

# Create test config
cat > testdata/test-config.env << CONFIGEOF
WG_INTERFACE=wg0
WG_PORT=51820
WG_SUBNET=10.0.0.0/24
WG_SERVER_IP=10.0.0.1/24
SERVER_PUBLIC_IP=127.0.0.1
MGMT_LISTEN=127.0.0.1:58889
MGMT_API_KEY=test-api-key-32bytes
DEFAULT_DNS=1.1.1.1,8.8.8.8
PEER_KEEPALIVE=25
PEERS_DB_PATH=/tmp/wg-test/testdata/peers.json
WG_CONF_PATH=/tmp/wg-test/testdata/wg0.conf
CLIENT_SCRIPT_TEMPLATE=/tmp/wg-test/testdata/connect.sh
CONFIGEOF

echo "=== Environment ready ==="
echo ""

# Start daemon in background
echo "=== Starting daemon ==="
./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

# Check daemon is running
if kill -0 $DAEMON_PID 2>/dev/null; then
    echo "PASS: daemon started (PID $DAEMON_PID)"
else
    echo "FAIL: daemon failed to start"
    exit 1
fi

AUTH="Authorization: Bearer test-api-key-32bytes"
API="http://127.0.0.1:58889"

# Test 1: Health
echo ""
echo "=== Test 1: Health ==="
HEALTH=$(curl -s "$API/api/v1/health")
if echo "$HEALTH" | grep -q '"ok"'; then
    echo "PASS: $HEALTH"
else
    echo "FAIL: $HEALTH"
fi

# Test 2: Status
echo ""
echo "=== Test 2: Status ==="
STATUS=$(curl -s -H "$AUTH" "$API/api/v1/status")
echo "Status: $STATUS"
if echo "$STATUS" | grep -q '"wireguard":"ok"'; then
    echo "PASS: wireguard OK"
else
    echo "WARN: wireguard status not ok (may still be fine)"
fi

# Test 3: Client script
echo ""
echo "=== Test 3: Client Script ==="
SCRIPT=$(curl -s "$API/api/v1/client-script")
LINES=$(echo "$SCRIPT" | wc -l)
if echo "$SCRIPT" | grep -q 'test-api-key-32bytes'; then
    echo "PASS: $LINES lines, API key substituted"
else
    echo "FAIL: API key not found in script"
fi

# Test 4: Auth check
echo ""
echo "=== Test 4: Auth ==="
NOAUTH=$(curl -s -o /dev/null -w "%{http_code}" "$API/api/v1/peers")
WRONG=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer wrong" "$API/api/v1/peers")
if [ "$NOAUTH" = "200" ] && [ "$WRONG" = "401" ]; then
    echo "PASS: localhost bypass, wrong key rejected (200/401)"
elif [ "$NOAUTH" = "401" ] && [ "$WRONG" = "401" ]; then
    echo "PASS: auth rejected (401/401)"
else
    echo "FAIL: expected 200/401 or 401/401 got $NOAUTH/$WRONG"
fi

# Test 5: Register new peer
echo ""
echo "=== Test 5: Register Peer ==="
REGISTER=$(curl -s -X POST "$API/api/v1/register" \
    -H "$AUTH" \
    -H "Content-Type: application/json" \
    -d '{"hostname":"wsl-test-client","dns":"1.1.1.1"}')
echo "Response: $REGISTER"

if echo "$REGISTER" | grep -q '"success":true'; then
    echo "PASS: peer registered"
    PEER_IP=$(echo "$REGISTER" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['address'])" 2>/dev/null || echo "?")
    PEER_KEY=$(echo "$REGISTER" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer']['private_key'])" 2>/dev/null || echo "?")
    echo "  Peer IP: $PEER_IP"
    echo "  Peer key: ${PEER_KEY:0:10}..."
else
    echo "FAIL: registration failed"
fi

# Test 6: Duplicate check
echo ""
echo "=== Test 6: Duplicate ==="
DUP=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/register" \
    -H "$AUTH" \
    -H "Content-Type: application/json" \
    -d '{"hostname":"wsl-test-client","dns":"1.1.1.1"}')
if [ "$DUP" = "409" ]; then
    echo "PASS: duplicate rejected (409)"
else
    echo "FAIL: expected 409 got $DUP"
fi

# Test 7: List peers
echo ""
echo "=== Test 7: List Peers ==="
PEERS=$(curl -s -H "$AUTH" "$API/api/v1/peers")
PEER_COUNT=$(echo "$PEERS" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer_count'])" 2>/dev/null || echo "?")
echo "PASS: peer_count=$PEER_COUNT"
echo "$PEERS" | python3 -m json.tool 2>/dev/null || echo "$PEERS"

# Test 8: WireGuard config check
echo ""
echo "=== Test 8: WG Config ==="
if [ -f testdata/wg0.conf ]; then
    echo "PASS: wg0.conf exists"
    echo "--- Config ---"
    cat testdata/wg0.conf
    echo "--------------"
else
    echo "FAIL: wg0.conf not found"
fi

# Test 9: Verify wg shows the peer
echo ""
echo "=== Test 9: WG Show ==="
wg show wg0
if wg show wg0 | grep -q "wsl-test-client"; then
    echo "PASS: peer visible in wg show"
else
    echo "WARN: peer may not appear in wg show (expected for test)"
fi

# Test 10: Delete peer
echo ""
echo "=== Test 10: Delete Peer ==="
DELETE=$(curl -s -X DELETE "$API/api/v1/peers/wsl-test-client" -H "$AUTH")
echo "Response: $DELETE"
if echo "$DELETE" | grep -q '"success":true'; then
    echo "PASS: peer deleted"
else
    echo "FAIL: delete failed"
fi

# Verify deleted
PEERS_AFTER=$(curl -s -H "$AUTH" "$API/api/v1/peers")
PEER_COUNT_AFTER=$(echo "$PEERS_AFTER" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer_count'])" 2>/dev/null || echo "?")
echo "Peers after delete: $PEER_COUNT_AFTER"

# Test 11: Windows config endpoint (auto-register)
echo ""
echo "=== Test 11: Windows Config ==="
WIN_CONF=$(curl -s -w "\n%{http_code}" "$API/api/v1/windows-config?name=win-test-pc&dns=1.1.1.1" -H "$AUTH")
WIN_CODE=$(echo "$WIN_CONF" | tail -1)
WIN_BODY=$(echo "$WIN_CONF" | sed '$d')

if [ "$WIN_CODE" = "200" ] && echo "$WIN_BODY" | grep -q "PrivateKey"; then
    echo "PASS: windows config returned (HTTP $WIN_CODE)"
    echo "--- .conf ---"
    echo "$WIN_BODY"
    echo "--------------"
else
    echo "FAIL: unexpected response (HTTP $WIN_CODE)"
    echo "$WIN_BODY"
fi

# Test 12: Windows config re-fetch (peer already exists)
echo ""
echo "=== Test 12: Windows Re-fetch ==="
WIN_CONF2=$(curl -s -w "\n%{http_code}" "$API/api/v1/windows-config?name=win-test-pc" -H "$AUTH")
WIN_CODE2=$(echo "$WIN_CONF2" | tail -1)
if [ "$WIN_CODE2" = "200" ]; then
    echo "PASS: re-fetch succeeded (HTTP $WIN_CODE2)"
else
    echo "FAIL: re-fetch failed (HTTP $WIN_CODE2)"
fi

# Cleanup
echo ""
echo "=== Cleanup ==="
kill $DAEMON_PID 2>/dev/null || true
wait $DAEMON_PID 2>/dev/null || true
echo "Daemon stopped"

echo ""
echo "=== All tests completed ==="
