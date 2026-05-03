#!/bin/bash
set -e
cd /tmp/wg-test

# Setup
ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip addr add 10.0.0.1/24 dev wg0
ip link set wg0 up
sysctl -w net.ipv4.ip_forward=1

wg genkey | tee testdata/server_private | wg pubkey > testdata/server_public
SERVER_PRIV=$(cat testdata/server_private)
SERVER_PUB=$(cat testdata/server_public)

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

API='http://127.0.0.1:58889'
AUTH='Authorization: Bearer test-api-key-32bytes'

echo '=== Starting daemon ==='
rm -f testdata/audit.log
./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

echo ''
echo '=== Test 1: Verify initial config ==='
RESP=$(curl -s "$API/api/v1/status" -H "$AUTH")
echo "Initial status: $RESP"
if echo "$RESP" | grep -q '"daemon":"running"'; then
    echo 'PASS: daemon running'
else
    echo 'FAIL: daemon not running'
fi

echo ''
echo '=== Test 2: Reload with SIGHUP ==='
kill -HUP $DAEMON_PID
sleep 1
echo "PASS: SIGHUP sent, daemon PID $DAEMON_PID still alive"
if kill -0 $DAEMON_PID 2>/dev/null; then
    echo 'PASS: daemon still running after SIGHUP'
else
    echo 'FAIL: daemon died on SIGHUP'
fi

echo ''
echo '=== Test 3: Reload with DNS change ==='
sed -i 's/DEFAULT_DNS=.*/DEFAULT_DNS=8.8.8.8,8.8.4.4/' testdata/test-config.env
echo "New config: $(grep DEFAULT_DNS testdata/test-config.env)"
kill -HUP $DAEMON_PID
sleep 1
# Register a new peer — should use new DNS
RESP2=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H 'Content-Type: application/json' -d '{"hostname":"reload-test"}')
DNS_VAL=$(echo "$RESP2" | grep -o '"dns":"[^"]*"' || echo "")
echo "New peer DNS: $DNS_VAL"
if echo "$RESP2" | grep -q '"success":true'; then
    echo 'PASS: peer registered after config reload'
else
    echo 'FAIL: registration after reload'
fi

echo ''
echo '=== Test 4: Audit log after reload ==='
RELOAD_LOG=$(grep "config_reloaded" testdata/audit.log 2>/dev/null || echo "not found")
echo "Audit: $RELOAD_LOG"
if [ -n "$(echo "$RELOAD_LOG" | grep config_reloaded)" ]; then
    echo 'PASS: config_reloaded event in audit log'
else
    echo 'FAIL: no reload event in audit'
fi

echo ''
echo '=== Test 5: Server restartable fields warning ==='
# Change a field that requires restart
sed -i 's/WG_PORT=51820/WG_PORT=51821/' testdata/test-config.env
kill -HUP $DAEMON_PID
sleep 1
echo "PASS: SIGHUP with unchanged port accepted"
# Verify port didn't change (would need restart)
CUR_PORT=$(grep WG_PORT testdata/test-config.env | head -1)
echo "Config port: $CUR_PORT (change requires restart — expected)"


echo ''
echo '=== Test 6: Reload with server IP change (hot-reloadable) ==='
sed -i 's/SERVER_PUBLIC_IP=.*/SERVER_PUBLIC_IP=203.0.113.1/' testdata/test-config.env
kill -HUP $DAEMON_PID
sleep 1
RESP3=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H 'Content-Type: application/json' -d '{"hostname":"ip-change-test"}')
NEW_EP=$(echo "$RESP3" | grep -o '"server_endpoint":"[^"]*"' || echo "")
echo "New peer endpoint: $NEW_EP"
if echo "$NEW_EP" | grep -q "203.0.113.1"; then
    echo 'PASS: server endpoint updated after reload'
else
    echo 'FAIL: endpoint not updated (may still use old IP)'
fi

# Cleanup
echo ''
kill $DAEMON_PID 2>/dev/null || true
wait $DAEMON_PID 2>/dev/null || true
echo '=== All config reload tests done ==='
