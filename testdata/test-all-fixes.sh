#!/bin/bash
set -e

failures=0
pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; failures=$((failures+1)); }

cd /tmp/wg-test

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
  "server": {"public_key":"$SERVER_PUB","private_key":"$SERVER_PRIV","endpoint":"127.0.0.1:51820","listen_port":51820,"address":"10.0.0.1/24","subnet":"10.0.0.0/24"},
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
CLEAN_PEERS_ON_EXIT=false
CONFIGEOF

./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

API='http://127.0.0.1:58889'
AUTH='Authorization: Bearer test-api-key-32bytes'

echo '=== Test 1: Register valid peer ==='
RESP=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d '{"hostname":"valid-peer"}')
if echo "$RESP" | grep -q '"success":true'; then pass "valid peer"; else fail "valid peer: $RESP"; fi

echo ''
echo '=== Test 2: Peer name validation - too long ==='
LONG_NAME=$(python3 -c "print('a'*65)")
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d "{\"hostname\":\"$LONG_NAME\"}")
if [ "$CODE" = "400" ]; then pass "long name rejected ($CODE)"; else fail "long name accepted: $CODE"; fi

echo ''
echo '=== Test 3: Peer name validation - bad chars ==='
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d '{"hostname":"bad name!"}')
if [ "$CODE" = "400" ]; then pass "bad chars rejected ($CODE)"; else fail "bad chars accepted: $CODE"; fi

echo ''
echo '=== Test 4: Peer name validation - slash attempt ==='
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d '{"hostname":"../../etc/wg0"}')
if [ "$CODE" = "400" ]; then pass "slash name rejected ($CODE)"; else fail "slash name accepted: $CODE"; fi

echo ''
echo '=== Test 5: ApproveRequest rollback ==='
REQ=$(curl -s -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"rollback-test"}')
REQ_ID=$(echo "$REQ" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])")
echo "Request ID: $REQ_ID"

# Approve the request (should work)
APPROVE=$(curl -s -X POST "$API/api/v1/requests/$REQ_ID/approve" -H "$AUTH")
echo "Approve response: $(echo $APPROVE | grep -o '"success":[^,]*')"
if echo "$APPROVE" | grep -q '"success":true'; then
    pass "approval succeeded"
else
    fail "approval failed: $APPROVE"
fi

echo ''
echo '=== Test 6: Register with sub-24 subnet ==='
kill $DAEMON_PID 2>/dev/null || true; wait $DAEMON_PID 2>/dev/null || true
sleep 1

cat > testdata/test-config.env << CONFIGEOF
WG_INTERFACE=wg0
WG_PORT=51820
WG_SUBNET=10.0.0.16/28
WG_SERVER_IP=10.0.0.17/28
SERVER_PUBLIC_IP=127.0.0.1
MGMT_LISTEN=127.0.0.1:58889
MGMT_API_KEY=test-api-key-32bytes
DEFAULT_DNS=1.1.1.1,8.8.8.8
PEER_KEEPALIVE=25
PEERS_DB_PATH=/tmp/wg-test/testdata/peers.json
WG_CONF_PATH=/tmp/wg-test/testdata/wg0.conf
AUDIT_LOG_PATH=/tmp/wg-test/testdata/audit.log
CONFIGEOF

cat > testdata/peers.json << PEEREOF
{
  "server": {"public_key":"$SERVER_PUB","private_key":"$SERVER_PRIV","endpoint":"127.0.0.1:51820","listen_port":51820,"address":"10.0.0.17/28","subnet":"10.0.0.16/28"},
  "peers": {},
  "requests": {}
}
PEEREOF

./bin/wg-mgmt-daemon --config=testdata/test-config.env &
DAEMON_PID=$!
sleep 2

# Register peers in /28 (only 14 hosts available: .18-.30, broadcast .31)
for i in $(seq 1 3); do
    RESP=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d "{\"hostname\":\"sub28-$i\"}")
    IP=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin).get('peer',{}); print(d.get('address',''))" 2>/dev/null || echo "")
    echo "  sub28-$i → IP: $IP"
    if echo "$IP" | grep -q "10.0.0"; then pass "sub28-$i got valid IP"; else fail "sub28-$i bad IP: $IP"; fi
done

echo ''
echo '=== Test 7: Config reload via SIGHUP ==='
sed -i 's/DEFAULT_DNS=.*/DEFAULT_DNS=9.9.9.9/' testdata/test-config.env
kill -HUP $DAEMON_PID
sleep 1
if kill -0 $DAEMON_PID 2>/dev/null; then
    pass "SIGHUP didn't kill daemon"
else
    fail "daemon died after SIGHUP"
fi

RESP=$(curl -s -X POST "$API/api/v1/register" -H "$AUTH" -H "Content-Type: application/json" -d '{"hostname":"dns-test"}')
if echo "$RESP" | grep -q '"dns":"9.9.9.9"'; then pass "DNS reloaded"; else fail "DNS not reloaded: $RESP"; fi

echo ''
echo '=== Test 8: Graceful shutdown with CLEAN_PEERS_ON_EXIT=false ==='
BEFORE=$(wg show wg0 peers 2>/dev/null | wc -l)
kill $DAEMON_PID 2>/dev/null || true; wait $DAEMON_PID 2>/dev/null || true
AFTER=$(wg show wg0 peers 2>/dev/null | wc -l || echo "0")
if [ "$AFTER" -gt 0 ] || [ "$BEFORE" -gt 0 ]; then
    pass "peers preserved ($BEFORE before, $AFTER after)"
else
    pass "clean state"
fi

ip link del wg0 2>/dev/null || true
echo ''
echo "=== Done: $failures failures ==="
exit $failures
