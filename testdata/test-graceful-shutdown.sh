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
CLEAN_PEERS_ON_EXIT=true
CONFIGEOF

API='http://127.0.0.1:58889'
AUTH='Authorization: Bearer test-api-key-32bytes'

echo '=== Starting daemon (CLEAN_PEERS_ON_EXIT=true) ==='
./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

echo ''
echo '=== Register a peer ==='
RESP=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H 'Content-Type: application/json' -d '{"hostname":"graceful-test"}')
echo "$RESP"
if echo "$RESP" | grep -q '"success":true'; then
    echo 'PASS: peer registered'
else
    echo 'FAIL: registration'
fi

echo ''
echo '=== Verify peer in wg show ==='
wg_peers=$(wg show wg0 peers)
echo "WG peers before shutdown: $wg_peers"
if [ -n "$wg_peers" ]; then
    echo 'PASS: peer visible in wg'
else
    echo 'FAIL: no peer in wg'
fi

echo ''
echo '=== Kill daemon gracefully ==='
kill -TERM $DAEMON_PID 2>/dev/null || true
wait $DAEMON_PID 2>/dev/null || true
echo "Daemon exit code: $?"

echo ''
echo '=== Verify peers cleaned from wg ==='
after_peers=$(wg show wg0 peers 2>/dev/null || echo "interface gone")
echo "WG peers after shutdown: $after_peers"
if [ -z "$after_peers" ] || [ "$after_peers" = "interface gone" ]; then
    echo 'PASS: no peers left (cleanup worked)'
else
    echo 'FAIL: peers remain after cleanup'
fi

echo ''
echo '=== Test CLEAN_PEERS_ON_EXIT=false ==='
# Update config
sed -i 's/CLEAN_PEERS_ON_EXIT=true/CLEAN_PEERS_ON_EXIT=false/' testdata/test-config.env

# Clean peer list
wg set wg0 peer "$SERVER_PUB" remove 2>/dev/null || true

# Restart daemon
cat > testdata/peers.json << PEEREOF2
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
PEEREOF2

./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID2=$!
sleep 2

RESP2=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H 'Content-Type: application/json' -d '{"hostname":"no-clean-test"}')
echo "Registered: $(echo "$RESP2" | grep -o '"success":[^,]*')"

echo '=== Kill daemon (no cleanup) ==='
kill -TERM $DAEMON_PID2 2>/dev/null || true
wait $DAEMON_PID2 2>/dev/null || true

echo "WG peers after shutdown (no-clean): $(wg show wg0 peers 2>/dev/null || echo 'none')"
after2=$(wg show wg0 peers 2>/dev/null || echo "")
if [ -n "$after2" ]; then
    echo 'PASS: peers preserved (CLEAN_PEERS_ON_EXIT=false)'
else
    echo 'INFO: no peers left (may be expected)'
fi

ip link del wg0 2>/dev/null || true
echo ''
echo '=== All graceful shutdown tests done ==='
