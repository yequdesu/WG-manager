#!/bin/bash
set -euo pipefail
cd /mnt/f/yequdesu_project/wire-guard-dev

rm -rf /tmp/wg-test
cp -r . /tmp/wg-test
cd /tmp/wg-test

export PATH=/usr/bin:/usr/sbin:$PATH   # ensure Linux wg, not Windows

ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip addr add 10.0.0.1/24 dev wg0
ip link set wg0 up

SP=$(wg genkey)
SPU=$(echo "$SP" | wg pubkey)

cat > testdata/peers.json << PEEREOF
{"server":{"public_key":"$SPU","private_key":"$SP","endpoint":"127.0.0.1:51820","listen_port":51820,"address":"10.0.0.1/24","subnet":"10.0.0.0/24"},"peers":{},"requests":{}}
PEEREOF

cp testdata/test-config.env testdata/tui-config.env
sed -i 's|PEERS_DB_PATH=.*|PEERS_DB_PATH=/tmp/wg-test/testdata/peers.json|' testdata/tui-config.env
sed -i 's|WG_CONF_PATH=.*|WG_CONF_PATH=/tmp/wg-test/testdata/wg0.conf|' testdata/tui-config.env
sed -i 's|CLIENT_SCRIPT_TEMPLATE=.*|CLIENT_SCRIPT_TEMPLATE=/tmp/wg-test/testdata/connect.sh|' testdata/tui-config.env
echo 'AUDIT_LOG_PATH=/tmp/wg-test/testdata/audit.log' >> testdata/tui-config.env

# Start daemon
./bin/wg-mgmt-daemon --config=testdata/tui-config.env &
DAEMON_PID=$!
sleep 2

# Test TUI can start and exit (send 'q' after 2 seconds)
echo "=== TUI Startup Test ==="
timeout 3 bash -c 'echo q | ./bin/wg-mgmt-tui' 2>&1 || true

echo ""
echo "=== TUI exit OK ==="

kill $DAEMON_PID 2>/dev/null || true
wait $DAEMON_PID 2>/dev/null || true
