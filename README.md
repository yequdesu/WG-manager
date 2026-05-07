# WG-Manager

WireGuard management layer — star-topology VPN with zero-touch client provisioning. A single Go daemon handles all peer lifecycle, with clients joining via one command. Includes an optional enhanced TUI dashboard built with Ratatui (Rust).

Supports **Linux / macOS / WSL / Windows / Mobile (QR)**.

---

## Table of Contents

- [Design](#design)
- [Quick Start](#quick-start)
  - [1. Server Setup](#1-server-setup)
  - [2. Open Ports](#2-open-ports)
  - [3. Clients Join](#3-clients-join)
  - [4. Admin Operations](#4-admin-operations)
- [Enhanced TUI Dashboard](#enhanced-tui-dashboard)
- [Logging & Diagnostics](#logging--diagnostics)
- [API Reference](#api-reference)
- [Updating](#updating)
- [Building](#building)
- [Troubleshooting](#troubleshooting)

---

## Design

Two connection modes for different trust levels:

| Mode | Trust | Use Case | Needs API Key |
|------|-------|----------|:--:|
| **Approval** (default) | Low / public | Public distribution, guest access, no key exposure | No |
| **Direct** | High / internal | Admin distributes to trusted devices, instant join | Yes (embedded server-side) |

```
┌─ Server ───────────────────────────────────────┐
│  nginx/caddy :443 (TLS termination)            │
│       │ proxy_pass localhost:58880              │
│  ┌────┴─────────────────────────────────────┐  │
│  │ wg-mgmt-daemon 127.0.0.1:58880           │  │
│  │ GET /connect   ← single entry for all     │  │
│  │ POST /register ← direct registration      │  │
│  │ POST /request  ← approval submission      │  │
│  │        │ wg set                            │  │
│  │ WireGuard wg0  10.0.0.1/24                │  │
│  └───────────────────────────────────────────┘  │
└────────┬──────────────┬─────────────────────────┘
         │ WG tunnel     │ HTTPS
      ┌──┴────┐    ┌────┴──────┐
      │ Linux │    │  Windows   │
      │ macOS │    │  PS / CMD  │
      │ WSL   │    │  Mobile QR │
      └───────┘    └────────────┘
```

---

## Quick Start

### 1. Server Setup

**Prerequisites:** Ubuntu/Debian Linux server with Git installed.

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager

# Install Go if needed
sudo apt install -y golang-go

# One-shot setup
sudo bash server/setup-server.sh
```

The script prompts for:

| Prompt | Description | Example |
|--------|-------------|---------|
| `Server Public IP` | Auto-detected, press Enter to confirm | `118.178.171.166` |
| `WireGuard Port` | WG listen port | `51820` (Enter for default) |
| `VPN Subnet` | VPN internal subnet | `10.0.0.0/24` (Enter for default) |
| `Management API Port` | Daemon HTTP port | `58880` (Enter for default) |
| `Default Client DNS` | Client DNS | `1.1.1.1,8.8.8.8` (Enter for default) |

After completion, the summary shows connection commands and the API Key.

**To upgrade the server later:**
```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh
# "Use existing configuration? [Y/n]" → Y + Enter
```

---

### 2. Open Ports

Add **inbound** rules in your cloud provider's security group:

| Protocol | Port | Purpose |
|----------|------|---------|
| UDP | 51820 | WireGuard tunnel |
| TCP | 443 | Management API (HTTPS via reverse proxy) |

> **Note:** The daemon binds to `127.0.0.1:58880` by default. For production, place a reverse proxy (nginx/caddy) on port 443 and forward to localhost. See [Production Deployment](#production-deployment-reverse-proxy--tls) for details. For quick dev/test setups, change `MGMT_LISTEN=0.0.0.0:58880` in `config.env` and open port 58880 instead.

If using UFW:
```bash
sudo ufw allow 51820/udp
sudo ufw allow 443/tcp
```

---

### 3. Clients Join

Replace `118.178.171.166` with your server IP in all commands below.

---

#### 3.1 Approval Mode (default, no API Key)

> Client submits request → Admin approves on server → Client auto-configures.

##### Linux / macOS / WSL

Open a terminal and run:

```bash
curl -sSf http://118.178.171.166:58880/connect | sudo bash
```

The script automatically:
1. Detects your OS
2. Installs WireGuard if needed (asks Y/n before installing)
3. Submits an access request (peer name defaults to hostname)
4. Polls every 3 seconds for the admin's decision
5. On approval: writes config, starts WireGuard, verifies connection

**Custom peer name:**
```bash
curl -sSf "http://118.178.171.166:58880/connect?name=my-device" | sudo bash
```

##### Windows PowerShell

```powershell
# Step 1: Download the script
Invoke-WebRequest http://118.178.171.166:58880/connect -OutFile join.ps1

# Step 2: Run (enter peer name when prompted)
.\join.ps1
```

The script submits a request, polls for approval, saves the `.conf` file on approval, and prints import instructions.

**After approval:**
1. Download [WireGuard for Windows](https://download.wireguard.com/windows-client/)
2. Open WireGuard → **Import Tunnel(s) from file**
3. Select the `.conf` file (e.g. `C:\Users\...\AppData\Local\Temp\wg0.conf`)
4. Click **Activate**

##### Windows CMD

```cmd
:: Step 1: Submit request
curl -X POST http://118.178.171.166:58880/api/v1/request ^
  -H "Content-Type: application/json" ^
  -d "{\"hostname\":\"MYPC\",\"dns\":\"1.1.1.1\"}"
:: Response: {"request_id":"abc123...","status":"pending"}
:: Note the request_id

:: Step 2: Poll status (repeat every few seconds)
curl -s http://118.178.171.166:58880/api/v1/request/abc123

:: Step 3: After admin approves, download .conf
curl -o wg0.conf "http://118.178.171.166:58880/connect?mode=direct&name=MYPC"

:: Step 4: Import into WireGuard client (same as PowerShell steps)
```

##### Mobile (QR Code)

WireGuard's official app has a built-in "Scan from QR code" feature. QR codes work in **direct mode** only:

**Admin generates QR on server:**
```bash
# Auto-registers peer "phone1" and outputs QR (phone scans directly)
curl -s "http://localhost:58880/connect?qrcode&mode=direct&name=phone1" -o phone1.svg
```

Share `phone1.svg` with the user → WireGuard App → Scan QR → connected.

---

#### 3.2 Direct Mode (admin-distributed, with API Key)

> Admin gives a URL to trusted users. They join instantly with no approval step.

The API Key is on the server only:
```bash
grep MGMT_API_KEY ~/WG-manager/config.env
```

##### Linux / macOS / WSL

```bash
curl -sSf "http://118.178.171.166:58880/connect?mode=direct&name=my-laptop" | sudo bash
```

The script auto-installs WG, registers with the embedded API Key, writes config, and connects.

##### Windows PowerShell

```powershell
Invoke-WebRequest "http://118.178.171.166:58880/connect?mode=direct&name=MYPC" -OutFile wg0.conf
```

Then import `wg0.conf` into the WireGuard client.

##### Windows CMD

```cmd
curl -o wg0.conf "http://118.178.171.166:58880/connect?mode=direct&name=MYPC"
```

---

#### 3.3 Verify Connection

On the client device:

```bash
sudo wg show              # Check WireGuard status
ping 10.0.0.1             # Ping the gateway (server)
ping 10.0.0.2             # Ping another peer
```

**Windows note:** If ping fails, allow ICMP:
```powershell
New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow
```

---

### 4. Admin Operations

All management is done on the server.

#### 4.1 TUI Dashboard (recommended)

```bash
wg-tui                 # Enhanced (if installed) or Legacy
wg-tui --legacy        # Force Legacy TUI
```

**Legacy TUI** (always available):

| Tab | Content | Actions |
|-----|---------|---------|
| **Peers** | All peers + detail panel (Name/IP/Key/Endpoint/HS) | `↑↓` select, `d` delete |
| **Requests** | Pending approval requests | `↑↓` select, `a` approve, `d` deny |
| **Status** | Server status + per-peer transfer stats | Read-only |
| **Log** | Last 50 audit log entries | `j/k` scroll |

Global: `Tab` switch tab, `r` refresh, `q` quit.

#### 4.2 Approve / Reject Requests (CLI)

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)

# View pending requests
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H "Authorization: Bearer $API_KEY" | python3 -m json.tool

# Approve (replace <id> with the actual request_id)
curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H "Authorization: Bearer $API_KEY"

# Reject
curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H "Authorization: Bearer $API_KEY"
```

#### 4.3 Delete a Peer

```bash
# TUI: Peers tab → ↑↓ select → d (confirm with d/y)

# CLI:
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H "Authorization: Bearer $API_KEY"
```

#### 4.4 Offline Peer Provisioning (Pre-generate Config)

Generate a client config locally on the server, auto-register the peer, then manually distribute the `.conf` file — no client HTTP request needed.

```bash
# Generate config for a new peer (saved to scripts/<name>.conf)
sudo bash ~/WG-manager/scripts/create-peer.sh --name my-laptop

# Custom DNS and output path
sudo bash ~/WG-manager/scripts/create-peer.sh --name office-pc --dns 8.8.8.8 -o /tmp/office.conf

# Distribute the .conf file to the client (email, USB, etc.)
# Client imports it into WireGuard — no approval or network request needed
```

The peer is automatically registered in the management system and added to the WireGuard interface on the server. The client only needs to import the `.conf` file and activate.

#### 4.5 Health Check

```bash
bash ~/WG-manager/scripts/health-check.sh
bash ~/WG-manager/scripts/list-peers.sh
```

---

## Enhanced TUI Dashboard

An optional enhanced TUI built with **Rust + Ratatui**, featuring a beautiful particle-physics background and smooth animations.

### Install

```bash
cd ~/WG-manager/wg-tui
bash install.sh --ustc          # Use USTC mirror in China
bash install.sh                  # Default
```

### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Tab` / `←→` | Switch tabs |
| `↑↓` / `j` `k` | Navigate lists |
| `/` | Search peers |
| `a` | Approve request |
| `d` / `y` | Delete peer / Deny request |
| `r` | Refresh data |
| `=` / `-` / `0` | Zoom in / out / reset |
| `Ctrl+Arrows` | Move window |
| `?` | Help overlay |
| `q` | Quit |

### Cross-Compilation (Windows → Linux)

```bash
# One-time setup
rustup target add x86_64-unknown-linux-musl

# Build Linux binary from any platform
cd wg-tui
bash build-linux.sh

# Deploy
scp wg-tui-ratatui-linux user@server:~/.local/bin/wg-tui-ratatui
ssh user@server 'chmod +x ~/.local/bin/wg-tui-ratatui'
```

---

## Logging & Diagnostics

### Unified Log

All events are written to `/var/log/wg-mgmt/wg-mgmt.log` with three modules:

| Module | Events |
|--------|--------|
| `[DAEMON]` | daemon started/stopped, peer registered/deleted, request lifecycle, config reloaded |
| `[WG]` | peer connected/disconnected, handshake complete, endpoint changed, keypair rotation, transfer milestones |
| `[HTTP]` | API write operations: POST/PUT/DELETE requests, non-localhost requests |

**Format:**
```
2026-05-03T15:00:00.123456Z [DAEMON] daemon_started version=1.0.0
2026-05-03T15:00:10.000000Z [WG] peer_connected peer=RoJ7SRMQC7Zu endpoint=112.49.240.57:16262
2026-05-03T15:00:15.456789Z [DAEMON] request_approved name=phone1 ip=10.0.0.7
```

The log rotates at 100 MB (keeps 10 archives). Routine TUI polling (GET requests from localhost) is filtered out.

### View Logs

```bash
tail -f /var/log/wg-mgmt/wg-mgmt.log
```

---

## API Reference

Base URL: `https://vpn.example.com` (reverse proxy) or `http://IP:58880` (direct)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/connect` | None | Dispatch: bash/ps1/conf/HTML/QR by User-Agent |
| `GET` | `/connect?qrcode` | None | SVG QR code (direct mode only) |
| `POST` | `/register` | KeyOrLocal | Register peer, return config |
| `POST` | `/request` | Rate-limited | Submit approval request |
| `GET` | `/request/{id}` | None | Poll status: pending / approved / rejected |
| `GET` | `/requests` | LocalOnly | List pending requests |
| `POST` | `/requests/{id}/approve` | LocalOnly | Approve request |
| `DELETE` | `/requests/{id}` | LocalOnly | Reject request |
| `GET` | `/peers` | LocalOnly | List peers with online status |
| `DELETE` | `/peers/{name}` | LocalOnly | Remove a peer |
| `GET` | `/status` | LocalOnly | Server + daemon status |
| `GET` | `/health` | None | Health check |

Auth explained:
- `LocalOnly` = accessible only from `127.0.0.1` (server local)
- `KeyOrLocal` = localhost bypass, or remote with `Authorization: Bearer <KEY>`

---

## Production Deployment (Reverse Proxy + TLS)

### Architecture

The daemon binds to `127.0.0.1:58880` by default. For production, place a reverse proxy (nginx or Caddy) in front that terminates TLS and forwards to the daemon on localhost. This isolates the daemon from direct internet exposure and centralises TLS certificate management.

```
                   ┌─────────────────────────────┐
  Internet ── TLS ─→  nginx / caddy :443         │
                   │  ┌───────────────────────┐  │
                   │  │ Terminates TLS        │  │
                   │  │ Splits public/admin   │  │
                   │  └───────┬───────────────┘  │
                   │          │ localhost:58880   │
                   │  ┌───────┴───────────────┐  │
                   │  │ wg-mgmt-daemon        │  │
                   │  │ 127.0.0.1:58880       │  │
                   │  └───────────────────────┘  │
                   └─────────────────────────────┘
```

### Route Isolation

The daemon enforces two access tiers. The reverse proxy must expose only public routes to the internet:

**Public routes** (safe for HTTPS exposure, no API key required):
| Route | Description |
|-------|-------------|
| `/api/v1/health` | Health check |
| `/api/v1/login` | User login (session-based auth) |
| `/api/v1/logout` | User logout |
| `/api/v1/redeem` | Redeem an invite token |
| `/api/v1/request` | Submit approval request (rate-limited) |
| `/api/v1/request/{id}` | Poll request status |
| `/connect` | Client join scripts (bash/ps1/conf/HTML/QR) |
| `/bootstrap` | Invite bootstrap script (token-based) |

**Admin routes** (daemon-enforced localhost-only — do NOT expose via proxy):
| Route | Description |
|-------|-------------|
| `/api/v1/requests` | List pending requests |
| `/api/v1/requests/{id}/approve` | Approve request |
| `/api/v1/requests/{id}` (DELETE) | Reject request |
| `/api/v1/peers` | List peers |
| `/api/v1/peers/{name}` (DELETE) | Remove peer |
| `/api/v1/status` | Server status |

The daemon's `LocalOnly` middleware (see `internal/api/middleware.go`) rejects any request to admin routes from non-localhost sources, regardless of the reverse proxy configuration.

### TLS Requirement

All production deployments MUST terminate TLS at the reverse proxy. The daemon itself speaks plain HTTP — encryption is the proxy's responsibility. Use Let's Encrypt (certbot / Caddy auto) for free certificates.

### Example: nginx

```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    ssl_certificate     /etc/letsencrypt/live/vpn.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vpn.example.com/privkey.pem;

    # ── Public routes (forward to daemon) ──
    location /api/v1/health      { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/login       { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/logout      { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/redeem      { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/request     { proxy_pass http://127.0.0.1:58880; }
    location /connect            { proxy_pass http://127.0.0.1:58880; }
    location /bootstrap          { proxy_pass http://127.0.0.1:58880; }

    # ── Block admin routes at the proxy level ──
    location /api/v1/requests    { return 403; }
    location /api/v1/peers       { return 403; }
    location /api/v1/status      { return 403; }

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

# Redirect HTTP → HTTPS
server {
    listen 80;
    server_name vpn.example.com;
    return 301 https://$host$request_uri;
}
```

### Example: Caddy

```
vpn.example.com {
    reverse_proxy /api/v1/health  127.0.0.1:58880
    reverse_proxy /api/v1/login   127.0.0.1:58880
    reverse_proxy /api/v1/logout  127.0.0.1:58880
    reverse_proxy /api/v1/redeem  127.0.0.1:58880
    reverse_proxy /api/v1/request 127.0.0.1:58880
    reverse_proxy /connect        127.0.0.1:58880
    reverse_proxy /bootstrap      127.0.0.1:58880

    # Block admin routes
    respond /api/v1/requests  403
    respond /api/v1/peers     403
    respond /api/v1/status    403
}
```

### Bootstrap URL

With the reverse proxy in place, the canonical bootstrap URL is:

```
https://vpn.example.com/bootstrap?token=INVITE_TOKEN&name=MYDEVICE
```

Users pipe this directly into bash. The script is served as plain text — users can (and should) inspect it before running:

```bash
# Inspect
curl -sSf https://vpn.example.com/bootstrap?token=TOKEN&name=my-device

# Run
curl -sSf "https://vpn.example.com/bootstrap?token=TOKEN&name=my-device" | sudo bash
```

The bootstrap script contains **no global API key** — the invite token is the sole credential, and it is consumed on first use (one-time redeem).

---

## Updating

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh   # Y to reuse config, auto-rebuilds if source changed

# Optional: rebuild enhanced TUI
cd wg-tui && bash install.sh
```

Existing WireGuard connections are **not interrupted** during updates.

---

## Building

### Go Daemon + Legacy TUI

```bash
make build      # Daemon → bin/wg-mgmt-daemon
make build-tui  # Legacy TUI → bin/wg-tui-legacy
make build-all  # Both
make vet        # go vet
```

### Enhanced TUI (Rust)

```bash
cd wg-tui
cargo build --release    # → target/release/wg-tui(.exe)
bash build-linux.sh      # Cross-compile for Linux (musl static binary)
```

---

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| Windows can't ping | `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| API unreachable | Check reverse proxy is running and cloud security group allows TCP 443 |
| No handshake | Check cloud security group allows UDP 51820 |
| Duplicate name (409) | Delete the old peer first, then rejoin |
| "Binary is up to date" but changes missing | `sudo rm -f /usr/local/bin/wg-mgmt-daemon` then re-run setup-server.sh |
| Daemon fails to start | `journalctl -u wg-mgmt -n 20` |
| WG interface missing | `modprobe wireguard && ip link add wg0 type wireguard` |
| `?name=` not working | Daemon binary may be stale — run `sudo bash server/setup-server.sh` |
| Audit log empty | Logrotate rotated the file — run `sudo systemctl kill -s HUP wg-mgmt` |
| Rust not found (wg-tui) | Install Rust: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` |
| `wg-tui` command not found | Both TUI variants share `/usr/local/bin/wg-tui` launcher — re-run setup |

---

## License

MIT
