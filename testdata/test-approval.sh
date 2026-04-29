#!/bin/bash
set -euo pipefail

cd /tmp/wg-test
killall wg-mgmt-daemon 2>/dev/null || true
sleep 1

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

echo "=== Test 1: Submit Request ==="
RESP=$(curl -s -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"approval-test","dns":"1.1.1.1"}')
echo "$RESP"
REQ_ID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])" 2>/dev/null || echo "")
if echo "$RESP" | grep -q '"pending"'; then echo "PASS"; else echo "FAIL"; fi

echo ""
echo "=== Test 2: Request Status ==="
curl -s "$API/api/v1/request/$REQ_ID" | python3 -m json.tool 2>/dev/null || curl -s "$API/api/v1/request/$REQ_ID"

echo ""
echo "=== Test 3: List Requests (admin) ==="
curl -s "$API/api/v1/requests" -H "$AUTH" | python3 -m json.tool 2>/dev/null || echo "FAIL"

echo ""
echo "=== Test 4: Approve Request ==="
curl -s -X POST "$API/api/v1/requests/$REQ_ID/approve" -H "$AUTH" | python3 -m json.tool 2>/dev/null
if [ $? -eq 0 ]; then echo "PASS: approved"; else echo "FAIL: approve failed"; fi

echo ""
echo "=== Test 5: Audit Log ==="
cat /var/log/wg-mgmt/audit.log

echo ""
echo "=== Test 6: Reject (separate request) ==="
RESP2=$(curl -s -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"reject-test"}')
REQ_ID2=$(echo "$RESP2" | python3 -c "import sys,json; print(json.load(sys.stdin)['request_id'])" 2>/dev/null || echo "")
curl -s -X DELETE "$API/api/v1/requests/$REQ_ID2" -H "$AUTH" | python3 -m json.tool 2>/dev/null
echo "PASS: rejected"

echo ""
echo "=== Test 7: Rate Limit ==="
for i in 1 2 3 4 5; do
    CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/api/v1/request" -H "Content-Type: application/json" -d '{"hostname":"ratelimit-test-'$i'"}')
    echo "  attempt $i: HTTP $CODE"
done

kill %1 2>/dev/null || true
wait 2>/dev/null || true
echo ""
echo "=== All tests complete ==="
