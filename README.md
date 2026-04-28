# WG-Manager

WireGuard management layer with HTTP API for automated peer provisioning.

A star-topology VPN where the Aliyun (or any Linux) server acts as the WireGuard hub, and client devices (Linux, macOS, Windows) join with a single command.

## Architecture

```
                          ┌──────────────────────────────────┐
                          │         Aliyun / Linux Server     │
                          │                                    │
                          │  ┌────────────────────────────┐   │
                          │  │   wg-mgmt-daemon (Go)       │   │
                          │  │   HTTP :58880               │   │
                          │  │                              │   │
                          │  │  Admin (127.0.0.1 only)     │   │
                          │  │  GET  /api/v1/peers         │   │
                          │  │  DELETE /api/v1/peers/:name │   │
                          │  │  GET  /api/v1/status        │   │
                          │  │                              │   │
                          │  │  Public (API Key auth)       │   │
                          │  │  POST /api/v1/register       │   │
                          │  │  GET  /api/v1/client-script  │   │
                          │  │  GET  /api/v1/windows-config │   │
                          │  │  GET  /api/v1/health         │   │
                          │  └─────────────┬──────────────┘   │
                          │                │ wg set / syncconf │
                          │  ┌─────────────┴──────────────┐   │
                          │  │      WireGuard (wg0)         │   │
                          │  │      10.0.0.1/24             │   │
                          │  └─────────────────────────────┘   │
                          └────────┬──────────────┬────────────┘
                                   │              │
                         wg tunnel │              │ HTTP :58880
                                   │              │
                    ┌──────────────┴─┐   ┌────────┴──────────┐
                    │  Linux / macOS  │   │     Windows        │
                    │  10.0.0.2       │   │     10.0.0.3       │
                    │                  │   │                    │
                    │  curl ... | bash │   │  curl → wg0.conf  │
                    └──────────────────┘   └────────────────────┘
```

## Features

- **Zero-touch client join** — Linux/macOS clients run a single curl command; Windows clients download a `.conf` file
- **No connection disruption** — adding/removing peers uses `wg set`, never restarting the WireGuard interface
- **Auto peer management** — key generation, IP allocation, config persistence handled automatically
- **Connection resilience** — `PersistentKeepalive` on both sides prevents NAT timeout; WireGuard's native roaming handles IP changes
- **Admin API** — list peers with online status, remove peers, check server health
- **Go daemon** — single static binary, no runtime dependencies, systemd-managed with auto-restart

## Quick Start

### 1. Server Setup (Ubuntu/Debian)

```bash
git clone git@github.com:yequdesu/WG-manager.git /root/wg-manager
cd /root/wg-manager

# Install Go if not present
apt install -y golang-go

# One-time setup
sudo bash server/setup-server.sh
```

Follow the prompts (public IP, ports, DNS). The script:
- Installs WireGuard if needed
- Enables IP forwarding
- Generates server keys
- Configures iptables FORWARD rules
- Compiles and starts the management daemon
- Prints client join commands

### 2. Allow Firewall Ports

In your cloud security group / firewall:

| Protocol | Port | Purpose |
|----------|------|---------|
| UDP | 51820 | WireGuard tunnel |
| TCP | 58880 | Management API |

```bash
ufw allow 51820/udp
ufw allow 58880/tcp
```

### 3. Clients Join

**Linux / macOS:**
```bash
curl -sSf http://<SERVER_IP>:58880/api/v1/client-script | sudo bash
```

**Windows (CMD):**
```cmd
curl -o wg0.conf "http://<SERVER_IP>:58880/api/v1/windows-config?name=%COMPUTERNAME%&key=<API_KEY>"
```
Then open WireGuard → Import Tunnel(s) → select `wg0.conf` → Connect.

**Windows (PowerShell):**
```powershell
Invoke-WebRequest "http://<SERVER_IP>:58880/api/v1/windows-config?name=$env:COMPUTERNAME&key=<API_KEY>" -OutFile wg0.conf
```

Both commands are displayed after running `setup-server.sh`.

## API Reference

Base URL: `http://<SERVER_IP>:58880`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/register` | API Key / localhost | Register a new peer, returns JSON with keys and config |
| `GET` | `/api/v1/client-script` | None | Returns auto-connect bash script (Linux/macOS) |
| `GET` | `/api/v1/windows-config?name=...&key=...` | API Key / localhost | Auto-register and return `.conf` for Windows client |
| `GET` | `/api/v1/peers` | API Key + localhost only | List all peers with online status |
| `DELETE` | `/api/v1/peers/:name` | API Key + localhost only | Remove a peer |
| `GET` | `/api/v1/status` | API Key + localhost only | Server and daemon status |
| `GET` | `/api/v1/health` | None | Health check (returns `{"status":"ok"}`) |

Auth: pass `Authorization: Bearer <API_KEY>` header or `?key=<API_KEY>` query parameter.

## Admin Commands

Run on the server:

```bash
# View all peers and their online status
curl -s http://127.0.0.1:58880/api/v1/peers \
  -H 'Authorization: Bearer <API_KEY>' | python3 -m json.tool

# Remove a peer (e.g., before client reinstall)
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H 'Authorization: Bearer <API_KEY>'

# Server status
curl -s http://127.0.0.1:58880/api/v1/status \
  -H 'Authorization: Bearer <API_KEY>'
```

Or use the bundled scripts:
```bash
bash scripts/list-peers.sh      # List peers
bash scripts/health-check.sh    # Health check
```

## Updating the Server

```bash
cd /root/wg-manager
git pull
sudo bash server/setup-server.sh   # Auto-detects config, rebuilds if source changed
```

Existing WireGuard connections are **not interrupted** during updates.

## Project Structure

```
wg-manager/
├── cmd/mgmt-daemon/main.go         # Daemon entry point
├── internal/
│   ├── api/                        # HTTP handlers, middleware, routing
│   │   ├── handler.go              # All API handlers
│   │   ├── server.go               # Route registration + auth middleware
│   │   ├── middleware.go            # Auth + admin-only middleware
│   │   └── template.go             # Client script template loader
│   ├── wg/manager.go               # WireGuard CLI operations (wg set, genkey, show)
│   └── store/peers.go              # Peer state persistence (JSON)
├── server/
│   ├── setup-server.sh             # One-shot server initialization + update
│   └── wg-mgmt.service             # systemd unit template
├── client/
│   ├── connect.sh                  # Linux/macOS auto-connect script template
│   ├── connect.ps1                 # Windows PowerShell helper
│   └── install-wireguard.sh        # Multi-OS WireGuard installer
├── scripts/
│   ├── build.sh / build.bat        # Cross-compile for Linux amd64
│   ├── list-peers.sh               # View all peers
│   └── health-check.sh             # Health monitoring
├── config.env                      # Central configuration template
└── Makefile
```

## Building

```bash
# Requires Go 1.21+
make build          # Linux amd64 binary → bin/wg-mgmt-daemon
make build-win      # Windows amd64 binary (for local testing)
make vet            # Run go vet
```

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| Windows can't ping server | Allow ICMP in Windows firewall: `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| Can't reach management API | Check cloud security group allows TCP on management port |
| Peer handshake never completes | Check cloud security group allows UDP on WireGuard port |
| Duplicate hostname error (409) | Delete the old peer first, then re-register |
| Daemon failed to start | `journalctl -u wg-mgmt -n 20` |
| WireGuard interface not found | `sudo modprobe wireguard && sudo ip link add wg0 type wireguard` |

## License

MIT
