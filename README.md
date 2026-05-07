# WG-Manager

WireGuard management layer for star-topology VPNs with invite-based onboarding. A single Go daemon handles peer lifecycle, identity, and HTTPS distribution. Users join by redeeming an invite token, no API key required. Includes an optional enhanced TUI dashboard (Rust + Ratatui).

Supports **Linux / macOS / WSL / Windows / Mobile (QR)**.

---

## Table of Contents

- [Design](#design)
- [Quick Start](#quick-start)
  - [1. Server Setup](#1-server-setup)
  - [2. First-Run: Create Owner](#2-first-run-create-owner)
  - [3. Create an Invite](#3-create-an-invite)
  - [4. User Onboarding](#4-user-onboarding)
  - [5. Admin Operations](#5-admin-operations)
- [Enhanced TUI Dashboard](#enhanced-tui-dashboard)
- [Logging & Diagnostics](#logging--diagnostics)
- [API Reference](#api-reference)
- [Production Deployment](#production-deployment)
- [Building](#building)
- [Updating](#updating)
- [Deprecation Notice](#deprecation-notice)
- [Troubleshooting](#troubleshooting)

---

## Design

### Onboarding Model (Single)

The system uses one onboarding model: invite-based. An owner or admin creates an invite token. The user redeems it, which atomically creates a peer and provisions WireGuard config. There is no approval queue, no direct registration, and no public registration endpoint.

```
owner/admin creates invite  -->  user redeems invite  -->  peer created atomically
```

### Role Model

| Role | Permissions |
|------|-------------|
| **owner** | Full access. Create/delete admins, manage invites, manage peers, system config. Created on first run via bootstrap password. |
| **admin** | Create/revoke invites, manage peers, view status. |
| **user** | Redeem invites, view own peer status. Created when an invite is redeemed. |

### Architecture

```
┌─ Server ───────────────────────────────────────┐
│  nginx/caddy :443 (TLS termination)            │
│       │ proxy_pass localhost:58880              │
│  ┌────┴─────────────────────────────────────┐  │
│  │ wg-mgmt-daemon 127.0.0.1:58880           │  │
│  │ POST /api/v1/login     ← session auth    │  │
│  │ POST /api/v1/redeem    ← invite redeem   │  │
│  │ GET  /bootstrap        ← join script     │  │
│  │ GET  /connect          ← browser portal  │  │
│  │        │ wg set                           │  │
│  │ WireGuard wg0  10.0.0.1/24               │  │
│  └──────────────────────────────────────────┘  │
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

**Prerequisites:** Ubuntu/Debian Linux server with Git installed, a domain name pointing to the server (for HTTPS).

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

After completion, the summary shows the bootstrap owner password and instructions for next steps.

**To upgrade the server later:**
```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh
# "Use existing configuration? [Y/n]" -> Y + Enter
```

---

### 2. First-Run: Create Owner

On first run, the daemon checks if any users exist. If none are found and `BOOTSTRAP_OWNER_PASSWORD` is set in `config.env`, it creates an owner account named `admin` with that password.

```bash
# Check the generated bootstrap password
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env
```

Save this password. You will use it to log in and create invites.

---

### 3. Create an Invite

Once logged in as owner or admin, create an invite for each user:

**Via API:**
```bash
curl -s -X POST http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Response includes the invite token and a bootstrap URL:

```json
{
  "token": "inv_abc123...",
  "url": "https://vpn.example.com/bootstrap?token=inv_abc123..."
}
```

**Via TUI:**
Open the TUI dashboard, navigate to the Invites tab, and create a new invite.

---

### 4. User Onboarding

Share the bootstrap URL with the user. The token is one-time use and consumed on first redeem.

#### Linux / macOS / WSL

```bash
curl -sSf "https://vpn.example.com/bootstrap?token=inv_abc123&name=my-device" | sudo bash
```

The script automatically:
1. Detects your OS
2. Installs WireGuard if needed (asks Y/n before installing)
3. Redeems the invite token
4. Writes WireGuard config
5. Starts the tunnel
6. Verifies connectivity

**Custom peer name:**
```bash
curl -sSf "https://vpn.example.com/bootstrap?token=inv_abc123&name=my-laptop" | sudo bash
```

#### Windows PowerShell

```powershell
# Download the bootstrap script
Invoke-WebRequest "https://vpn.example.com/bootstrap?token=inv_abc123&name=MYPC" -OutFile join.ps1

# Run it
.\join.ps1
```

#### Windows CMD

```cmd
curl -o wg0.conf "https://vpn.example.com/bootstrap?token=inv_abc123&name=MYPC"
```

Then import `wg0.conf` into the WireGuard client.

#### Mobile (QR Code)

Admins can generate QR codes from the server:

```bash
# Generate QR for an invite token
curl -s "http://localhost:58880/api/v1/invites/qrcode?token=inv_abc123&name=phone1" \
  -H "Authorization: Bearer $API_KEY" \
  -o phone1.svg
```

Share `phone1.svg` with the user. They scan it with the WireGuard mobile app and connect.

---

### 5. Admin Operations

All management is done on the server or via the management API.

#### 5.1 TUI Dashboard (recommended)

```bash
wg-tui                 # Rust Ratatui dashboard, if installed
```

The TUI shows tabs for Peers, Invites, Users, Status, and Log.

#### 5.2 Create an Invite (CLI)

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)

curl -s -X POST http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"max_uses": 1}'
```

#### 5.3 List / Revoke Invites

```bash
# List all invites
curl -s http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY"

# Revoke an invite
curl -s -X DELETE http://127.0.0.1:58880/api/v1/invites/inv_abc123 \
  -H "Authorization: Bearer $API_KEY"
```

#### 5.4 Manage Peers

```bash
# List peers
curl -s http://127.0.0.1:58880/api/v1/peers \
  -H "Authorization: Bearer $API_KEY"

# Delete a peer
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H "Authorization: Bearer $API_KEY"
```

TUI: Peers tab, select with arrow keys, press `d` to delete.

#### 5.5 Health Check

```bash
bash ~/WG-manager/scripts/health-check.sh
bash ~/WG-manager/scripts/list-peers.sh
```

---

## Enhanced TUI Dashboard

An optional enhanced TUI built with **Rust + Ratatui**, featuring a particle-physics background and smooth animations.

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
| `d` / `y` | Delete peer / Revoke invite |
| `r` | Refresh data |
| `=` / `-` / `0` | Zoom in / out / reset |
| `Ctrl+Arrows` | Move window |
| `?` | Help overlay |
| `q` | Quit |

### Cross-Compilation (Windows to Linux)

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
| `[DAEMON]` | daemon started/stopped, peer registered/deleted, invite lifecycle, config reloaded |
| `[WG]` | peer connected/disconnected, handshake complete, endpoint changed, keypair rotation, transfer milestones |
| `[HTTP]` | API write operations: POST/PUT/DELETE requests, non-localhost requests |

**Format:**
```
2026-05-03T15:00:00.123456Z [DAEMON] daemon_started version=1.0.0
2026-05-03T15:00:10.000000Z [WG] peer_connected peer=RoJ7SRMQC7Zu endpoint=112.49.240.57:16262
2026-05-03T15:00:15.456789Z [DAEMON] invite_redeemed token=inv_abc name=phone1 ip=10.0.0.7
```

The log rotates at 100 MB (keeps 10 archives). Routine TUI polling (GET requests from localhost) is filtered out.

### View Logs

```bash
tail -f /var/log/wg-mgmt/wg-mgmt.log
```

---

## API Reference

Base URL: `https://vpn.example.com` (reverse proxy) or `http://IP:58880` (direct, local network only).

### Public Routes

Accessible over HTTPS with no authentication (except login/redeem which handle auth internally).

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/health` | None | Health check |
| `POST` | `/api/v1/login` | None | Log in with username + password, receive session cookie |
| `POST` | `/api/v1/logout` | Session | Log out |
| `POST` | `/api/v1/redeem` | None | Redeem an invite token, receive WireGuard config |
| `GET` | `/bootstrap` | None | Bootstrap script (pipe to bash) |
| `GET` | `/connect` | None | Browser portal or script dispatch by User-Agent |

### Admin Routes

Localhost-only (enforced by daemon middleware). Do not expose via reverse proxy.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/peers` | LocalOnly | List all peers |
| `DELETE` | `/api/v1/peers/{name}` | LocalOnly | Remove a peer |
| `GET` | `/api/v1/invites` | LocalOnly | List all invites |
| `POST` | `/api/v1/invites` | LocalOnly | Create a new invite |
| `GET` | `/api/v1/invites/qrcode` | LocalOnly | SVG QR code for invite token |
| `DELETE` | `/api/v1/invites/{token}` | LocalOnly | Revoke an invite |
| `GET` | `/api/v1/users` | LocalOnly | List users |
| `DELETE` | `/api/v1/users/{name}` | LocalOnly | Remove a user |
| `GET` | `/api/v1/status` | LocalOnly | Server + daemon status |

Auth explained:
- `LocalOnly` = accessible only from `127.0.0.1` (server local). Use SSH or a local terminal.
- `None` = no auth required (public endpoint).
- `Session` = valid session cookie required.

---

## Production Deployment

### Architecture

The daemon binds to `127.0.0.1:58880` by default. For production, place a reverse proxy (nginx or Caddy) in front that terminates TLS and forwards to the daemon on localhost.

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

The daemon enforces two access tiers via middleware. The reverse proxy must expose only public routes to the internet.

**Public routes** (safe for HTTPS exposure):
| Route | Description |
|-------|-------------|
| `/api/v1/health` | Health check |
| `/api/v1/login` | User login |
| `/api/v1/logout` | User logout |
| `/api/v1/redeem` | Redeem invite token |
| `/bootstrap` | Invite bootstrap script |
| `/connect` | Client join scripts / browser portal |

**Admin routes** (localhost-only, do not expose via proxy):
| Route | Description |
|-------|-------------|
| `/api/v1/peers` | List/manage peers |
| `/api/v1/invites` | List/create/revoke invites |
| `/api/v1/users` | List/manage users |
| `/api/v1/status` | Server status |

### TLS Requirement

All production deployments MUST terminate TLS at the reverse proxy. The daemon speaks plain HTTP. Use Let's Encrypt (certbot or Caddy auto) for free certificates.

### Example: nginx

```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    ssl_certificate     /etc/letsencrypt/live/vpn.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vpn.example.com/privkey.pem;

    # Public routes (forward to daemon)
    location /api/v1/health      { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/login       { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/logout      { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/redeem      { proxy_pass http://127.0.0.1:58880; }
    location /bootstrap          { proxy_pass http://127.0.0.1:58880; }
    location /connect            { proxy_pass http://127.0.0.1:58880; }

    # Block admin routes at the proxy level
    location /api/v1/peers       { return 403; }
    location /api/v1/invites     { return 403; }
    location /api/v1/users       { return 403; }
    location /api/v1/status      { return 403; }

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

# Redirect HTTP to HTTPS
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
    reverse_proxy /bootstrap      127.0.0.1:58880
    reverse_proxy /connect        127.0.0.1:58880

    # Block admin routes
    respond /api/v1/peers   403
    respond /api/v1/invites 403
    respond /api/v1/users   403
    respond /api/v1/status  403
}
```

### Bootstrap URL

With the reverse proxy in place, the canonical bootstrap URL is:

```
https://vpn.example.com/bootstrap?token=INVITE_TOKEN&name=MYDEVICE
```

Users pipe this directly into bash. The script is served as plain text. Users should inspect it before running:

```bash
# Inspect
curl -sSf https://vpn.example.com/bootstrap?token=TOKEN&name=my-device

# Run (only after inspecting)
curl -sSf "https://vpn.example.com/bootstrap?token=TOKEN&name=my-device" | sudo bash
```

The bootstrap script contains no global API key. The invite token is the sole credential and is consumed on first use.

---

## Building

### Go Daemon

```bash
make build      # Daemon -> bin/wg-mgmt-daemon
make build-all  # Daemon
make vet        # go vet ./...
```

### Enhanced TUI (Rust)

```bash
cd wg-tui
cargo build --release    # -> target/release/wg-tui(.exe)
bash build-linux.sh      # Cross-compile for Linux (musl static binary)
```

---

## Updating an Existing Server

This path upgrades a server that is already running an older WG-Manager version. Existing WireGuard tunnels keep working because the update preserves `/etc/wireguard/wg0.conf` and `server/peers.json`.

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh
# When prompted: "Use existing configuration? [Y/n]" -> Y + Enter

# Optional: install or rebuild the Rust TUI
cd ~/WG-manager/wg-tui && bash install.sh
```

After upgrading from the old approval/direct model:

```bash
# 1. Confirm the daemon is listening only on localhost
grep MGMT_LISTEN ~/WG-manager/config.env

# 2. Save the generated owner password for first login
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env

# 3. Restart and inspect service health
sudo systemctl restart wg-mgmt
sudo systemctl status wg-mgmt --no-pager
```

Then configure HTTPS reverse proxying to `http://127.0.0.1:58880`, create new invites, and replace any old scripts that call `/api/v1/register`, `/api/v1/request`, or `/connect?mode=direct`.

---

## Deprecation Notice

### Removed Endpoints

The following endpoints are deprecated and return HTTP 410 (Gone). They are retained only to signal migration to clients:

| Old Endpoint | Status | Replacement |
|---|---|---|
| `POST /api/v1/register` | 410 Gone | `POST /api/v1/redeem` with an invite token |
| `POST /api/v1/request` | 410 Gone | `POST /api/v1/redeem` |
| `GET /api/v1/request/{id}` | 410 Gone | None (no polling needed) |
| `GET /api/v1/requests` | 410 Gone | `GET /api/v1/invites` |
| `POST /api/v1/requests/{id}/approve` | 410 Gone | `POST /api/v1/invites` to create invite instead |
| `DELETE /api/v1/requests/{id}` | 410 Gone | `DELETE /api/v1/invites/{token}` |

### Removed Config Options

| Config Key | Reason |
|---|---|
| `CLEAN_PEERS_ON_EXIT` | No longer supported. Peers persist across restarts. |

### Removed Query Parameters

| Parameter | Details |
|---|---|
| `?mode=direct` | Removed. All onboarding is invite-based. |
| `?mode=approval` | Removed. Approval flow is replaced by invites. |

### Migration Path for Existing Deployments

If you are upgrading from a version that used the approval flow:

1. Legacy pending approval requests are ignored by the new daemon; recreate access through invites.
2. Create invites for your existing users via `POST /api/v1/invites`.
3. Share the invite URLs with users. They can join by redeeming their invite.
4. Old peers on the server continue to work. No action needed for existing WireGuard connections.
5. Remove any scripts or documentation that reference the old endpoints.

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
| Invite token invalid | Tokens are one-time use. Check if already redeemed via `curl http://127.0.0.1:58880/api/v1/invites -H "Authorization: Bearer $KEY"` |
| Audit log empty | Logrotate rotated the file. Run `sudo systemctl kill -s HUP wg-mgmt` |
| Rust not found (wg-tui) | Install Rust: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` |
| `wg-tui` command not found | Install the Rust TUI: `cd ~/WG-manager/wg-tui && bash install.sh` |

---

## License

MIT
