#!/bin/bash
set -e
cd /tmp/wg-test

# Setup WireGuard interface
ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip addr add 10.0.0.1/24 dev wg0
ip link set wg0 up
sysctl -w net.ipv4.ip_forward=1

# Generate server keys
wg genkey | tee testdata/server_private | wg pubkey > testdata/server_public
SERVER_PRIV=$(cat testdata/server_private)
SERVER_PUB=$(cat testdata/server_public)

# Create peers.json
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
  "requests": {}
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
AUDIT_LOG_PATH=/tmp/wg-test/testdata/audit.log
CONFIGEOF

# Start daemon
echo 'Starting daemon...'
./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

API='http://127.0.0.1:58889'
AUTH='Authorization: Bearer test-api-key-32bytes'

echo ''
echo '=== Test 1: Auth with query param (should FAIL 401) ==='
CODE_Q=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/v1/register?key=test-api-key-32bytes" -H 'Content-Type: application/json' -d '{"hostname":"query-test"}')
echo "HTTP $CODE_Q"
if [ "$CODE_Q" = "401" ]; then
    echo 'PASS: query param auth rejected'
else
    echo 'FAIL: expected 401'
fi

echo ''
echo '=== Test 2: Auth with Bearer header (should PASS 200) ==='
RESP=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H 'Content-Type: application/json' -d '{"hostname":"header-test"}')
echo "Response: $RESP"
if echo "$RESP" | grep -q '"success":true'; then
    echo 'PASS: Bearer header auth works'
else
    echo 'FAIL: register with Bearer header failed'
fi

echo ''
echo '=== Test 3: No auth (should FAIL 401) ==='
CODE_N=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/v1/register" -H 'Content-Type: application/json' -d '{"hostname":"noauth-test"}')
echo "HTTP $CODE_N"
if [ "$CODE_N" = "401" ]; then
    echo 'PASS: no auth rejected'
else
    echo 'FAIL: expected 401'
fi

echo ''
echo '=== Test 4: Localhost bypass (should still work) ==='
CODE_L=$(curl -s -o /dev/null -w '%{http_code}' "$API/api/v1/peers")
echo "HTTP $CODE_L"
if [ "$CODE_L" = "200" ]; then
    echo 'PASS: localhost bypass works'
else
    echo 'FAIL: expected 200'
fi

# Cleanup
kill $DAEMON_PID 2>/dev/null || true
wait $DAEMON_PID 2>/dev/null || true
echo ''
echo '=== All tests done ==='
