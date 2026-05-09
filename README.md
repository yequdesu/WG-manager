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
- [CLI Reference](#cli-reference)
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

### Capability Matrix

Three fixed roles: owner, admin, user. No custom roles.

| Action | Owner | Admin | User | Enforcement |
|--------|-------|-------|------|-------------|
| View own status (`GET /api/v1/me`) | ✅ | ✅ | ✅ | Session-based, remote accessible |
| Health, login, logout, redeem | ✅ | ✅ | ✅ | Public |
| Bootstrap, connect | ✅ | ✅ | ✅ | Public |
| List peers | ✅ | ✅ | ❌ | LocalOnly |
| Delete peer (by name or pubkey) | ✅ | ✅ | ❌ | LocalOnly |
| Set peer alias | ✅ | ✅ | ❌ | LocalOnly |
| View daemon status | ✅ | ✅ | ❌ | LocalOnly |
| List invites | ✅ | ✅ | ❌ | LocalOnly |
| Create invite | ✅ | ✅* | ❌ | LocalOnly |
| Invite QR code | ✅ | ✅ | ❌ | LocalOnly |
| View invite link/command | ✅ | ✅ | ❌ | LocalOnly |
| Revoke invite | ✅ | ✅ | ❌ | LocalOnly |
| Delete invite (soft-delete) | ✅ | ✅ | ❌ | LocalOnly |
| Force-delete invite | ✅ | ✅ | ❌ | LocalOnly |
| List users | ✅ | ❌ | ❌ | LocalOnly + Owner |
| Create user | ✅ | ❌ | ❌ | LocalOnly + Owner |
| Delete user | ✅ | ❌ | ❌ | LocalOnly + Owner |
| Bootstrap owner (first run) | ✅ | ❌ | ❌ | No users exist yet |

*Admin can create invites only with target_role="user". Cannot create owner-level invites or direct user accounts.

- **LocalOnly** = accessible only from `127.0.0.1`. Use SSH or a local terminal.
- **API key** (`MGMT_API_KEY`) bypasses role checks but is restricted by LocalOnly to prevent remote abuse.

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

**Prerequisites:** Ubuntu/Debian Linux server with Git installed. A domain name is optional but recommended (enables automatic HTTPS).

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager

# Install Go if needed
sudo apt install -y golang-go

# One-shot setup (installs daemon + CLI + systemd service)
sudo bash server/setup-server.sh
```

The script prompts for:

| Prompt | Description | Example |
|--------|-------------|---------|
| `Server Public IP` | Auto-detected, press Enter to confirm | `118.178.171.166` |
| `Server Domain (optional)` | Domain for HTTPS; press Enter to skip (uses IP + HTTP) | `vpn.example.com` |
| `WireGuard Port` | WG listen port | `51820` (Enter for default) |
| `VPN Subnet` | VPN internal subnet | `10.0.0.0/24` (Enter for default) |
| `Management API Port` | Daemon HTTP port | `58880` (Enter for default) |
| `Default Client DNS` | Client DNS | `1.1.1.1,8.8.8.8` (Enter for default) |

After completion:
- The CLI `wg-mgmt` is installed at `/usr/local/bin/wg-mgmt`
- The daemon `wg-mgmt-daemon` runs as a systemd service
- The summary shows the bootstrap owner password and next steps

#### Address Pools

You can divide the VPN subnet into named address ranges called pools. Peers assigned to a pool get IPs from that range. Add lines to `config.env` with the prefix `POOL_`:

```
POOL_CLIENTS=10.0.0.10-10.0.0.100
POOL_SERVERS=10.0.0.101-10.0.0.200
```

The format is `POOL_<NAME>=<startIP>-<endIP>`. When creating an invite, pass `pool_name` to assign the peer to that pool:

```bash
curl -s -X POST http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"pool_name": "CLIENTS"}'
```

Rules:
- Pools must not overlap with each other.
- Pools must not include the server IP (subnet .1).
- The entire range must lie within `WG_SUBNET`.
- Invalid pool config is logged as a warning and pools are disabled.

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

Response includes the invite token and a ready-to-use bootstrap URL (scheme and host depend on your server configuration):

```json
{
  "token": "inv_abc123...",
  "url": "https://YOUR_DOMAIN/bootstrap?token=inv_abc123..."
}
```

If you configured a domain, the URL uses `https://YOUR_DOMAIN/`. Without a domain, it falls back to `http://YOUR_SERVER_IP/`.

**Via TUI:**
Open the TUI dashboard, navigate to the Invites tab, and create a new invite.

---

### 4. User Onboarding

Share the bootstrap URL with the user. The token is one-time use and consumed on first redeem.

#### Linux / macOS / WSL

Replace `BOOTSTRAP_URL` below with the URL from the invite creation step.

```bash
curl -sSf "BOOTSTRAP_URL" | sudo bash
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
curl -sSf "BOOTSTRAP_URL&name=my-laptop" | sudo bash
```

**WSL note:** On WSL, the script warns that WireGuard should be installed on the Windows host (not inside WSL). Follow the Windows PowerShell or CMD path instead.

#### Windows PowerShell

```powershell
# Download the bootstrap script
Invoke-WebRequest "BOOTSTRAP_URL&name=MYPC" -OutFile join.ps1

# Run it
.\join.ps1
```

#### Windows CMD

```cmd
curl -o wg0.conf "BOOTSTRAP_URL&name=MYPC"
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

## CLI Reference

The `wg-mgmt` CLI runs on the server and talks to the daemon over localhost. It reads `MGMT_LISTEN` and `MGMT_API_KEY` from `config.env` by default. The setup script installs it automatically at `/usr/local/bin/wg-mgmt`.

```bash
wg-mgmt --help
# Usage: wg-mgmt [--config FILE] <command>
```

### Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config FILE` | `config.env` | Path to config.env |

### Commands

| Command | Description |
|---------|-------------|
| `peer` | List, alias, delete peers |
| `invite` | Create, list, view links, revoke, delete, force-delete invites and generate QR codes |
| `user` | List, create, delete users |
| `status` | Daemon and WireGuard status |
| `auth` | Login and logout (session management) |
| `me` | Current user info |

---

### peer list

Lists all peers with their public key, alias, name, IP, online status, and endpoint.

```bash
# Table output (default)
wg-mgmt peer list

# JSON output
wg-mgmt peer list --format json
```

Example table output:

```
  PUBLIC KEY       ALIAS   NAME     IP          ONLINE   ENDPOINT
  RoJ7SRMQC7Zu     my-lap  my-lap   10.0.0.2    online   112.49.240.57:16262
  aB3cDeFgHiJk     phone   phone1   10.0.0.3    offline
```

### peer alias

Sets a friendly alias for a peer, identified by its immutable public key.

```bash
wg-mgmt peer alias --id <public_key> --alias <new_name>
```

Example:

```bash
wg-mgmt peer alias --id RoJ7SRMQC7Zu... --alias "John's Laptop"
# Alias updated: "" -> "John's Laptop" (peer: my-lap)
```

### peer delete

Deletes a peer by its public key. Ambiguous alias-only delete is rejected.

```bash
wg-mgmt peer delete --id RoJ7SRMQC7Zu...
```

---

### invite create

Creates a new invite token with optional constraints.

```bash
wg-mgmt invite create [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--ttl N` | `72` | TTL in hours |
| `--pool NAME` | - | Address pool name |
| `--dns SERVERS` | - | Custom DNS servers |
| `--role ROLE` | - | Target role (`user` or `admin`) |
| `--max-uses N` | `1` | Maximum redemption count |
| `--labels K=V,...` | - | Comma-separated key=value labels |
| `--name-hint TEXT` | - | Display name hint for the invite |
| `--device-name NAME` | - | Pre-bound device name |
| `--format FORMAT` | `human` | Output format (`human` or `json`) |

Human output includes the token, expiry, a copyable bootstrap URL, and a ready-to-pipe curl command:

```
Invite created: inv_abc123...
Token:       inv_abc123...
Expires:     2026-05-10T15:00:00Z

Bootstrap URL (share this with the client):
  https://YOUR_DOMAIN/bootstrap?token=inv_abc123...

Copy-and-paste command:
  curl -sSf "https://YOUR_DOMAIN/bootstrap?token=inv_abc123..." | sudo bash
```

### invite list

Lists all invites with their status, hint, issuer, and redemption info.

```bash
wg-mgmt invite list [--format human|json] [--show-deleted]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `human` | Output format |
| `--show-deleted` | `false` | Include soft-deleted invites |

Human output example:

```
ID           STATUS    NAME HINT   ISSUED BY   EXPIRES             REDEEMED
inv_abc123   created   my-laptop   admin       2026-05-10T15:...   -
inv_def456   redeemed  phone1      admin       -                   2026-05-08T10:... by john
```

### invite revoke

Revokes an invite by its ID. Redeemed invites cannot be revoked.

```bash
wg-mgmt invite revoke --id <invite_id>
# Docker-style shorthand also works:
wg-mgmt invite revoke <id_prefix_or_name_hint>
```

### invite delete

Soft-deletes an invite (marks as deleted but preserves history).

```bash
wg-mgmt invite delete --id <invite_id>
# Docker-style shorthand also works:
wg-mgmt invite delete <id_prefix_or_name_hint>
```

### invite link

Shows the full bootstrap URL and copy-paste onboarding command for an issued invite. New invites can be looked up by invite ID because the server retains the raw token locally; legacy invites created before this change may require the original raw token.

If the invite was created with `--device-name`, that name is reused in the generated URL. Otherwise the URL omits `name`, and the bootstrap script uses the client's hostname. Passing `--name` explicitly overrides the stored device name.

```bash
# Docker-style shorthand also works with the short ID shown by invite list:
wg-mgmt invite link <id_prefix_or_name_hint>
# Override the peer name explicitly if needed:
wg-mgmt invite link <id_prefix_or_name_hint> --name <device_name>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--id` | No | Invite ID or raw token; can also be passed as a positional argument |
| `--name` | No | Override device name embedded in the bootstrap URL |
| `--format` | No | Output format (`human` or `json`) |

### invite force-delete

Permanently removes an invite in any state. This is irreversible and requires a second confirmation value matching the invite ID.

```bash
wg-mgmt invite force-delete --id <invite_id> --confirm <invite_id>
# Prefixes/name hints are resolved like Docker, but confirmation is still required:
wg-mgmt invite force-delete <id_prefix_or_name_hint> --confirm <same_prefix_or_full_id>
```

### invite qrcode

Generates an SVG QR code for a bootstrap URL from an invite token.

```bash
wg-mgmt invite qrcode --id <token> --name <device_name> --output <file.svg>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--id` | Yes | Invite token (raw token from invite create output) |
| `--name` | No | Device name embedded in the QR URL; omitted by default so bootstrap uses the client hostname |
| `--output` | Yes | Output SVG file path |

---

### user list

Lists all users with their name, role, and creation time. Requires the API key (owner-only).

```bash
# Table output (default)
wg-mgmt user list

# JSON output
wg-mgmt user list --format json
```

### user create

Creates a new user account. Requires the API key (owner-only).

```bash
wg-mgmt user create --name <username> --password <password> [--role <role>]
```

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--name` | Yes | - | Username |
| `--password` | Yes | - | Password |
| `--role` | No | `user` | Role (`owner`, `admin`, `user`) |

### user delete

Deletes a user by name. Requires the API key (owner-only).

```bash
wg-mgmt user delete --name <username>
```

---

### auth login

Authenticates with the daemon and receives a session token.

```bash
wg-mgmt auth login [--name <username> --password <password>]
```

When run without flags, prompts interactively for username and password. If the CLI already has an API key configured, it informs you that you are already authenticated.

Output includes the session token to store in `MGMT_SESSION_TOKEN`.

### auth logout

Logs out the current session.

```bash
wg-mgmt auth logout
```

---

### status

Shows daemon and WireGuard interface status.

```bash
# Human-readable
wg-mgmt status

# JSON
wg-mgmt status --format json
```

Human output example:

```
daemon: running
wireguard: ok
interface: wg0
listen_port: 51820
peers: 3 online / 5 total
```

### me

Shows the currently authenticated user's name, role, and creation time. Requires a session token.

```bash
# Use MGMT_SESSION_TOKEN env var or --session-token flag
export MGMT_SESSION_TOKEN=<session_token>
wg-mgmt me

# JSON
wg-mgmt me --format json
```

Example:

```
name: admin
role: owner
created_at: 2026-05-03T15:00:00Z
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
| `v` | View selected invite URL and onboarding command |
| `F` | Force-delete selected invite after confirmation |
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

Base URL: `https://YOUR_DOMAIN` (reverse proxy, HTTPS) or `http://SERVER_IP:58880` (direct, local network only).

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
| `GET` | `/api/v1/invites/{id}/link` | LocalOnly | Show bootstrap URL and onboarding command |
| `DELETE` | `/api/v1/invites/{id}` | LocalOnly | Revoke an invite |
| `DELETE` | `/api/v1/invites/{id}?action=force-delete` | LocalOnly | Permanently force-delete an invite |
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

### Deployment Automation

The `server/setup-server.sh` script automates daemon installation, WireGuard init, and systemd service setup. After that, run the guided proxy deployment script:

```bash
# For nginx (default):
sudo bash server/deploy-proxy.sh

# For Caddy:
sudo bash server/deploy-proxy.sh --caddy
```

The `deploy-proxy.sh` script handles everything:

- **With a domain**: Sets up nginx or Caddy with TLS (Let's Encrypt), generates HTTPS config, and validates before reloading.
- **Without a domain**: Generates an HTTP-only config and warns that HTTPS is unavailable. Bootstrap URLs use `http://SERVER_IP/`.
- **Safety**: Backs up existing configs, validates generated config (`nginx -t` or `caddy validate`), and rolls back on failure.
- **Admin routes**: Blocks `/api/v1/peers`, `/api/v1/invites`, `/api/v1/users`, `/api/v1/status` at the proxy level (returns 403).

### TLS Strategy

The daemon itself speaks plain HTTP on localhost. TLS is handled by the reverse proxy.

| Scenario | Behavior |
|----------|----------|
| Domain configured + DNS resolves | Automatic HTTPS via Let's Encrypt (nginx: certbot standalone; Caddy: auto-ACME) |
| No domain or DNS not resolved | HTTP-only fallback with clear warning. Bootstrap URLs use `http://` over the public IP. |

### Manual Certificate Install (Aliyun SSL)

If you download Aliyun SSL files manually, place them in one folder. The preferred names are `<domain>.pem` and `<domain>.key` (for example `wg.yequdesu.top.pem` and `wg.yequdesu.top.key`); if those exact names are not present, the script can still auto-detect one unambiguous cert file (`*.pem`, `*.crt`, or `*.cer`) and one `*.key` file. Then install them with validation and rollback:

```bash
sudo bash server/install-cert.sh /path/to/certs wg.yequdesu.top
make install-cert CERT_DIR=/path/to/certs DOMAIN=wg.yequdesu.top
```

The script checks cert/key validity, confirms the public keys match, warns on missing or ambiguous files, warns on subject/SAN mismatch, shows the target paths before making changes, backs up any existing `/etc/letsencrypt/live/<domain>/` files, and rolls back automatically if `nginx -t` or reload steps fail.

### Example: nginx (generated by deploy-proxy.sh --nginx)

```nginx
server {
    listen 443 ssl http2;
    server_name YOUR_DOMAIN;

    ssl_certificate     /etc/letsencrypt/live/YOUR_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/YOUR_DOMAIN/privkey.pem;

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
    server_name YOUR_DOMAIN;
    return 301 https://$host$request_uri;
}
```

Without a domain, the script generates an HTTP-only config (no TLS, no redirect).

### Example: Caddy (generated by deploy-proxy.sh --caddy)

With domain (Caddy auto-provisions TLS):

```
YOUR_DOMAIN {
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

Without a domain, the script generates an `:80` config block (HTTP-only).

### Bootstrap URL

With the reverse proxy in place, the bootstrap URL uses your domain or server IP:

| Scenario | Bootstrap URL format |
|----------|---------------------|
| Domain + HTTPS | `https://YOUR_DOMAIN/bootstrap?token=TOKEN` |
| IP-only + HTTP | `http://YOUR_SERVER_IP/bootstrap?token=TOKEN` |
| Explicit device name | `https://YOUR_DOMAIN/bootstrap?token=TOKEN&name=DEVICE` |

Users pipe this directly into bash. The script is served as plain text. Users should inspect it before running:

```bash
# Inspect
curl -sSf "https://YOUR_DOMAIN/bootstrap?token=TOKEN"

# Run (only after inspecting)
curl -sSf "https://YOUR_DOMAIN/bootstrap?token=TOKEN" | sudo bash
```

The bootstrap script contains no global API key. The invite token is the sole credential and is consumed on first use.

---

## Building

### Go Daemon + CLI

```bash
make build       # Daemon -> bin/wg-mgmt-daemon
make build-cli   # CLI -> bin/wg-mgmt
make build-all   # Daemon + CLI
make vet         # go vet ./...
make clean       # Remove build artifacts
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
```

The setup script automatically:
- Rebuilds and reinstalls the daemon (`wg-mgmt-daemon`) if source has changed
- Rebuilds and reinstalls the CLI (`wg-mgmt` at `/usr/local/bin/wg-mgmt`)
- Preserves the existing config, API key, and bootstrap owner password
- Restarts the systemd service without interrupting existing WireGuard connections

**Optional:** rebuild the Rust TUI:
```bash
cd ~/WG-manager/wg-tui && bash install.sh
```

### State Migration

On startup, the daemon automatically reconciles its state with the live WireGuard interface:

1. **Peer recovery** - Peers present in WireGuard but missing from `peers.json` are recovered with a generated name.
2. **Missing peers** - Peers in `peers.json` but absent from WireGuard are re-added to the interface.
3. **Alias and invite migration** - If the existing state lacks alias or invite fields, they are backfilled automatically.
4. **Pool config** - If `POOL_*` entries exist in `config.env`, they are parsed and loaded at startup.

After the upgrade, the daemon log shows migration results:

```
State migration complete: 5 peer alias(es), 3 invite(s) backfilled
Loaded 2 address pool(s)
```

### Post-Upgrade Steps

```bash
# 1. Confirm the daemon is listening only on localhost
grep MGMT_LISTEN ~/WG-manager/config.env

# 2. Save the generated owner password for first login
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env

# 3. Restart and inspect service health
sudo systemctl restart wg-mgmt
sudo systemctl status wg-mgmt --no-pager

# 4. (Optional) Add address pools if needed
# Edit config.env and add: POOL_NAME=startIP-endIP
# Then reload without restart:
sudo systemctl kill -s HUP wg-mgmt
```

### Upgrading from Old Approval / Direct Model

If you are upgrading from a version that used the old approval or direct registration flow:

1. The old endpoints (`/api/v1/register`, `/api/v1/request`, etc.) now return `410 Gone`.
2. Active peers on the server continue to work. No action needed for existing connections.
3. Legacy pending approval requests are ignored. Recreate access through invites.
4. Create invites for your existing users via `POST /api/v1/invites` or the TUI.
5. Configure HTTPS reverse proxying to `http://127.0.0.1:58880`.
6. Replace any old scripts that call old endpoints with the invite-based flow.

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
| WSL bootstrap issues | WSL is detected by the bootstrap script. A warning recommends installing WireGuard on the Windows host, not inside WSL. Use the Windows PowerShell or CMD path instead. |
| Bootstrap fails with parser error | The bootstrap script tries `jq`, then `python3`, then a basic `grep`/`sed` fallback before redeeming. If all parsers fail, install `jq` or `python3`; if HTTP 200 was already returned, ask an admin for a fresh invite and share the raw response. |

---

## License

MIT
