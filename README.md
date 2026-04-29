# WG-Manager

WireGuard management layer — star-topology VPN with automated client provisioning. The server runs a Go daemon with an HTTP API; clients join with a single command.

## Architecture

```
                          ┌───────────────────────────────┐
                          │       Server (Linux)            │
                          │                                 │
                          │  ┌─────────────────────────┐   │
                          │  │  wg-mgmt-daemon (Go)     │   │
                          │  │  HTTP :58880              │   │
                          │  │                           │   │
                          │  │  GET /connect             │   │ ← unified entry
                          │  │  POST /register           │   │
                          │  │  POST /request            │   │
                          │  │  GET /peers               │   │
                          │  │  GET /health              │   │
                          │  └───────────┬───────────────┘   │
                          │              │ wg set             │
                          │  ┌───────────┴───────────────┐   │
                          │  │    WireGuard (wg0)         │   │
                          │  │    10.0.0.1/24             │   │
                          │  └───────────────────────────┘   │
                          └──────┬─────────────┬─────────────┘
                                 │             │
                     wg tunnel   │             │ HTTP :58880
                                 │             │
               ┌─────────────────┴──┐  ┌───────┴────────────┐
               │  Linux / macOS /   │  │      Windows        │
               │  WSL               │  │                     │
               │                    │  │  curl → wg0.conf    │
               │  curl | sudo bash  │  │  iwr → .ps1         │
               └────────────────────┘  └─────────────────────┘
```

## Features

- **One command join** — `curl http://IP:58880/connect | sudo bash` for all platforms
- **Approval workflow** — untrusted clients submit requests, admin approves via `wg-mgmt-tui`
- **Direct mode** — trusted clients get a URL with embedded API key, joining instantly
- **Zero disruption** — adding/removing peers uses `wg set`, never restarts the interface
- **Auto peer management** — key generation, IP allocation, config persistence
- **TUI dashboard** — terminal-based management: view peers, approve requests, watch logs
- **Audit logging** — all events logged to `/var/log/wg-mgmt/audit.log` with logrotate
- **Go daemon** — single static binary, systemd-managed, auto-restart

## Quick Start

### 1. Server Setup

```bash
git clone git@github.com:yequdesu/WG-manager.git /root/wg-manager
cd /root/wg-manager
sudo bash server/setup-server.sh
```

### 2. Allow Firewall Ports

| Protocol | Port | Purpose |
|----------|------|---------|
| UDP | 51820 | WireGuard tunnel |
| TCP | 58880 | Management API |

### 3. Clients Join

**All platforms — approval mode (default, no API key):**

| Platform | Command |
|----------|---------|
| Linux / macOS / WSL | `curl -sSf http://IP:58880/connect \| sudo bash` |
| Windows PowerShell | `iwr http://IP:58880/connect -OutFile t.ps1; .\t.ps1` |
| Browser | Open `http://IP:58880/connect` → HTML dispatch page |

The client submits a request. An admin approves it via `wg-mgmt-tui`. The client auto-configures on approval.

**Direct mode (admin-distributed URL with embedded API key):**

| Platform | Command |
|----------|---------|
| Linux / macOS / WSL | `curl -sSf "http://IP:58880/connect?mode=direct&name=DEVICE" \| sudo bash` |
| Windows | `curl -o wg0.conf "http://IP:58880/connect?mode=direct&name=MYPC"` |

## Admin Commands

```bash
wg-mgmt-tui                          # TUI dashboard
tail -f /var/log/wg-mgmt/audit.log   # Audit log
bash scripts/health-check.sh         # Health check
bash scripts/list-peers.sh           # List peers (CLI)
```

**Approve / reject requests:**
```bash
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H 'Authorization: Bearer <KEY>' | python3 -m json.tool

curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H 'Authorization: Bearer <KEY>'

curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H 'Authorization: Bearer <KEY>'
```

**Delete a peer:**
```bash
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H 'Authorization: Bearer <KEY>'
```

## API Reference

Base URL: `http://IP:58880`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/connect` | None | Unified dispatch — returns bash/ps1/conf/HTML based on User-Agent |
| `POST` | `/register` | API Key / localhost | Register peer, returns config |
| `POST` | `/request` | Rate-limited | Submit approval request |
| `GET` | `/request/{id}` | None | Poll request status (pending / approved / rejected) |
| `GET` | `/requests` | API Key + localhost | List pending requests |
| `POST` | `/requests/{id}/approve` | API Key + localhost | Approve a request |
| `DELETE` | `/requests/{id}` | API Key + localhost | Reject a request |
| `GET` | `/peers` | API Key + localhost | List peers with online status |
| `DELETE` | `/peers/{name}` | API Key + localhost | Remove a peer |
| `GET` | `/status` | API Key + localhost | Server status |
| `GET` | `/health` | None | Health check |

## Project Structure

```
wg-manager/
├── cmd/
│   ├── mgmt-daemon/main.go          # Daemon entry point
│   └── mgmt-tui/main.go             # TUI entry point
├── internal/
│   ├── api/                         # HTTP handlers, middleware, routing
│   ├── audit/                       # Audit logging
│   ├── store/                       # Peer/request state (JSON)
│   └── wg/                          # WireGuard CLI operations
├── client/
│   ├── connect.sh                   # Direct join script template
│   ├── request-approval.sh          # Approval join script template
│   ├── request-approval.ps1         # Windows approval script
│   ├── install-wireguard.sh         # Multi-OS WG installer
│   └── lib/os-detect.sh            # Platform abstraction layer
├── server/
│   ├── setup-server.sh              # One-shot init + update
│   └── wg-mgmt.service              # systemd unit
├── scripts/
│   ├── build.sh / build.bat         # Cross-compile
│   ├── list-peers.sh / health-check.sh
├── config.env
└── Makefile
```

## Building

```bash
make build          # Linux amd64 → bin/wg-mgmt-daemon
make build-tui      # TUI binary → bin/wg-mgmt-tui
make build-all      # Both binaries
make vet            # Run go vet
```

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| Windows can't ping | Allow ICMP: `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| API unreachable | Check cloud security group allows TCP 58880 |
| No handshake | Check cloud security group allows UDP 51820 |
| Duplicate name (409) | Delete the old peer first, then rejoin |
| Daemon fails to start | `journalctl -u wg-mgmt -n 20` |
| WG interface not found | `modprobe wireguard && ip link add wg0 type wireguard` |

## License

MIT
