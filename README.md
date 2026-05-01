# WG-Manager

WireGuard management layer — star-topology VPN with zero-touch client provisioning. A single Go daemon handles all peer lifecycle, with clients joining via one command. Supports Linux / macOS / WSL / Windows / Mobile (QR).

---

## Table of Contents

- [Design](#design)
- [Quick Start](#quick-start)
  - [1. Server Setup](#1-server-setup)
  - [2. Open Ports](#2-open-ports)
  - [3. Clients Join](#3-clients-join)
  - [4. Admin Operations](#4-admin-operations)
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
┌─ Server ──────────────────────────────────┐
│  wg-mgmt-daemon :58880                     │
│  GET /connect   ← single entry for all     │
│  POST /register ← direct registration      │
│  POST /request  ← approval submission      │
│         │ wg set                            │
│  WireGuard wg0  10.0.0.1/24                │
└────┬──────────────┬────────────────────────┘
     │ WG tunnel     │ HTTP
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
| TCP | 58880 | Management API |

If using UFW:
```bash
sudo ufw allow 51820/udp
sudo ufw allow 58880/tcp
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

**Download first for interactive name prompt:**
```bash
curl -sSf http://118.178.171.166:58880/connect -o join.sh
sudo bash join.sh
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

WireGuard's official app has a built-in "Scan from QR code" feature. WG-Manager provides QR codes in two ways:

**Scenario A: Printed after PC approval**

When an approval completes on Linux / WSL / PowerShell, the terminal prints a QR command:

```bash
# After approval on Linux/WSL:
# Mobile: curl "http://118.178.171.166:58880/connect?qrcode&mode=direct&name=DEVICE" -o qr.svg
#   → scan with WireGuard app
```

Run that command to get an SVG file. Send it to the phone → WireGuard App → Scan QR → tunnel imports directly.

**Scenario B: Admin generates directly on server**

```bash
# Auto-registers peer "alice-phone" and outputs QR
curl -s "http://localhost:58880/connect?qrcode&mode=direct&name=alice-phone" -o alice.svg
```

Share `alice.svg` with the user → WireGuard App → Scan QR → connected.

---

#### 3.2 Direct Mode (admin-distributed, with API Key)

> Admin gives a URL to trusted users. They join instantly with no approval step.

The API Key is on the server only:
```bash
grep MGMT_API_KEY ~/WG-manager/config.env
# MGMT_API_KEY=9c4a9e609690eef456f5ae6014e981cf22e6c01ad6f96df632b187e0376ac31d
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
wg-mgmt-tui
```

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
# TUI: Peers tab → ↑↓ select → d

# CLI:
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H "Authorization: Bearer $API_KEY"
```

#### 4.4 Health Check

```bash
bash ~/WG-manager/scripts/health-check.sh
bash ~/WG-manager/scripts/list-peers.sh
tail -f /var/log/wg-mgmt/audit.log
```

---

## API Reference

Base URL: `http://IP:58880`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/connect` | None | Dispatch: bash/ps1/conf/HTML/QR by User-Agent |
| `GET` | `/connect?qrcode` | None | SVG QR code |
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
- `KeyOrLocal` = localhost bypass, or remote with `Authorization: Bearer <KEY>` / `?key=<KEY>`

---

## Updating

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh   # Y to reuse config, auto-rebuilds if source changed
```

Existing WireGuard connections are **not interrupted** during updates.

---

## Building

```bash
make build      # Daemon → bin/wg-mgmt-daemon
make build-tui  # TUI → bin/wg-mgmt-tui
make build-all  # Both
make vet        # go vet
```

---

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| Windows can't ping | `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| API unreachable | Check cloud security group allows TCP 58880 |
| No handshake | Check cloud security group allows UDP 51820 |
| Duplicate name (409) | Delete the old peer first, then rejoin |
| "Binary is up to date" but changes missing | `sudo rm -f /usr/local/bin/wg-mgmt-daemon` then re-run setup-server.sh |
| Daemon fails to start | `journalctl -u wg-mgmt -n 20` |
| WG interface missing | `modprobe wireguard && ip link add wg0 type wireguard` |
| Pipe mode no prompt | Append `?name=MYNAME` to URL, or download script first: `curl -o t.sh ...; sudo bash t.sh` |
| Config port corrupted | `sed -i 's/MGMT_LISTEN=.*/MGMT_LISTEN=0.0.0.0:58880/' config.env; systemctl restart wg-mgmt` |

---

## License

MIT
