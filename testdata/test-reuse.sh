#!/bin/bash
set -euo pipefail
rm -rf /tmp/wg-test3
cp -r /mnt/f/yequdesu_project/wire-guard-dev /tmp/wg-test3
cd /tmp/wg-test3

export PATH=/usr/bin:/usr/sbin:$PATH
ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip addr add 10.0.0.1/24 dev wg0
ip link set wg0 up

SP=$(wg genkey)
SPU=$(echo "$SP" | wg pubkey)

cat > testdata/peers.json << EOF
{"server":{"public_key":"$SPU","private_key":"$SP","endpoint":"127.0.0.1:51820","listen_port":51820,"address":"10.0.0.1/24","subnet":"10.0.0.0/24"},"peers":{},"requests":{}}
EOF

mkdir -p /var/log/wg-mgmt
./bin/wg-mgmt-daemon --config=testdata/test-config.env &
sleep 2

AUTH="Authorization: Bearer test-api-key-local-32bytes"
API="http://127.0.0.1:58889"

echo "=== 1. Submit request ==="
REQ=$(curl -s -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"reuse-test","dns":"1.1.1.1"}')
echo "$REQ"
ID=$(echo "$REQ" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])" 2>/dev/null || echo "")
echo "Request ID: $ID"

echo ""
echo "=== 2. Approve ==="
curl -s -X POST "$API/api/v1/requests/$ID/approve" -H "$AUTH" | python3 -m json.tool 2>/dev/null || true

echo ""
echo "=== 3. Request status (approved) ==="
curl -s "$API/api/v1/request/$ID" | python3 -m json.tool 2>/dev/null || true

echo ""
echo "=== 4. Delete peer ==="
curl -s -X DELETE "$API/api/v1/peers/reuse-test" -H "$AUTH" | python3 -m json.tool 2>/dev/null || true

echo ""
echo "=== 5. Re-submit same name (should PASS) ==="
RESULT=$(curl -s -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"reuse-test","dns":"1.1.1.1"}')
if echo "$RESULT" | grep -q "pending"; then
    echo "PASS: re-submit accepted"
else
    echo "FAIL: $RESULT"
fi

kill %1 2>/dev/null || true
wait 2>/dev/null || true
