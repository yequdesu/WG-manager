# WG-Manager

WireGuard management layer — star-topology VPN with zero-touch client provisioning.

The server runs a Go daemon with an HTTP API. Clients join with a single command. Supports Linux / macOS / WSL / Windows.

## Design

Two connection modes to fit different trust levels:

| Mode | Trust | How | API Key |
|------|-------|-----|:--:|
| **Approval** (default) | Untrusted / public | Client submits request → admin approves → auto-configures | No |
| **Direct** | Trusted / internal | Admin gives a URL with embedded API key → instant join | Yes |

```
                     ┌───────────────────────┐
                     │      Server (Linux)    │
                     │                        │
                     │  wg-mgmt-daemon :58880 │
                     │  ┌──────────────────┐  │
                     │  │ GET /connect     │  │ ← single entry point
                     │  │ POST /register   │  │    for all platforms
                     │  │ POST /request    │  │
                     │  └──────────────────┘  │
                     │       wg set ↓         │
                     │  WireGuard wg0 10.0.0.1 │
                     └──┬─────────────┬───────┘
                        │ WG tunnel   │ HTTP
              ┌─────────┴───┐    ┌────┴──────────┐
              │ Linux/macOS │    │    Windows      │
              │ / WSL        │    │                 │
              │ curl｜sudo   │    │ iwr → .ps1      │
              │   bash       │    │ curl → .conf    │
              └──────────────┘    └─────────────────┘
```

## Quick Start

### 1. Server Setup

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager
sudo bash server/setup-server.sh
```

### 2. Open Ports

| Protocol | Port | Purpose |
|----------|------|---------|
| UDP | 51820 | WireGuard tunnel |
| TCP | 58880 | Management API |

### 3. Clients Join

**Approval mode (default, no API key):**

| Platform | Command |
|----------|---------|
| Linux / macOS / WSL | `curl -sSf http://IP:58880/connect \| sudo bash` |
| Windows PowerShell | `iwr http://IP:58880/connect -OutFile t.ps1; .\t.ps1` |
| Browser | Open `http://IP:58880/connect` |

**Direct mode (admin-distributed, embedded API key):**

| Platform | Command |
|----------|---------|
| Linux / macOS / WSL | `curl -sSf "http://IP:58880/connect?mode=direct&name=DEVICE" \| sudo bash` |
| Windows | `curl -o wg0.conf "http://IP:58880/connect?mode=direct&name=MYPC"` |

> **Note**: when using `curl | sudo bash` (pipe mode), stdin is already consumed by the script — interactive prompts are not possible. To set a custom peer name, append `?name=MYNAME` to the URL. For interactive name input, download the script first (`curl -o t.sh ...; sudo bash t.sh`).

## Admin Commands

```bash
wg-mgmt-tui                          # TUI dashboard
tail -f /var/log/wg-mgmt/audit.log   # Audit log
bash scripts/health-check.sh         # Health check
bash scripts/list-peers.sh           # List peers
```

**TUI keybindings:**

| Key | Action |
|-----|--------|
| `Tab` | Switch tab (Peers / Requests / Status / Log) |
| `↑ ↓` | Navigate list |
| `a` | Approve selected request |
| `d` | Delete selected peer / reject selected request |
| `r` | Refresh |
| `q` | Quit |

**CLI equivalents:**

```bash
# View pending requests
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H 'Authorization: Bearer <KEY>' | python3 -m json.tool

# Approve
curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H 'Authorization: Bearer <KEY>'

# Reject
curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H 'Authorization: Bearer <KEY>'

# Delete a peer
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H 'Authorization: Bearer <KEY>'
```

## API Reference

Base URL: `http://IP:58880`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/connect` | None | Dispatch: bash/ps1/conf/HTML by User-Agent |
| `POST` | `/register` | API Key / localhost | Register peer, returns config |
| `POST` | `/request` | Rate-limited | Submit approval request |
| `GET` | `/request/{id}` | None | Poll status: pending / approved / rejected |
| `GET` | `/requests` | API Key + localhost | List pending requests |
| `POST` | `/requests/{id}/approve` | API Key + localhost | Approve a request |
| `DELETE` | `/requests/{id}` | API Key + localhost | Reject a request |
| `GET` | `/peers` | API Key + localhost | List peers with online status |
| `DELETE` | `/peers/{name}` | API Key + localhost | Remove a peer |
| `GET` | `/status` | API Key + localhost | Server and daemon status |
| `GET` | `/health` | None | Health check |

Auth: `Authorization: Bearer <KEY>` header or `?key=<KEY>` query parameter. Admin endpoints also allow localhost access without authentication.

## Updating the Server

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh   # detects changes, rebuilds if needed
```

Existing WireGuard connections are **not interrupted** during updates.

## Project Structure

```
wg-manager/
├── cmd/
│   ├── mgmt-daemon/main.go          # Daemon entry (HTTP API + WG ops)
│   └── mgmt-tui/main.go             # Terminal UI entry
├── internal/
│   ├── api/                         # Handlers, middleware, routing, embedded scripts
│   ├── audit/                       # Audit log writer
│   ├── store/                       # Peer / Request state (peers.json)
│   └── wg/                          # WireGuard CLI operations (wg set / genkey / show)
├── client/
│   ├── connect.sh                   # Direct join script (embedded in daemon)
│   ├── request-approval.sh          # Approval join script (embedded in daemon)
│   ├── request-approval.ps1         # Windows approval script (embedded in daemon)
│   ├── install-wireguard.sh         # Standalone WG installer
│   └── lib/os-detect.sh            # Platform abstraction (reusable library)
├── server/
│   ├── setup-server.sh              # One-shot init / upgrade
│   └── wg-mgmt.service              # systemd unit
├── scripts/
│   ├── build.sh / build.bat         # Cross-compile helpers
│   └── list-peers.sh / health-check.sh
├── config.env
└── Makefile
```

## Building

```bash
make build          # Daemon binary → bin/wg-mgmt-daemon
make build-tui      # TUI binary → bin/wg-mgmt-tui
make build-all      # Both
make vet            # go vet
```

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| Windows can't ping server | Allow ICMP: `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| API unreachable | Check cloud security group allows TCP 58880 |
| No WireGuard handshake | Check cloud security group allows UDP 51820 |
| Duplicate peer name (409) | Delete the old peer first, then rejoin |
| Daemon fails to start | `journalctl -u wg-mgmt -n 20` |
| WG interface missing | `modprobe wireguard && ip link add wg0 type wireguard` |
| Config port was corrupted | `sed -i 's/MGMT_LISTEN=.*/MGMT_LISTEN=0.0.0.0:58880/' config.env` then `systemctl restart wg-mgmt` |
| Pipe mode no prompt | Use `?name=MYNAME` in URL, or download script then `sudo bash script.sh` |

## License

MIT
