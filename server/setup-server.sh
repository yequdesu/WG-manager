#!/bin/bash
set -euo pipefail

# WireGuard Management Layer - Server Setup Script
# Run as root on Ubuntu/Debian server

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()    { echo -e "${GREEN}[+]${NC} $*"; }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; }
error()  { echo -e "${RED}[x]${NC} $*"; }
info()   { echo -e "${CYAN}[i]${NC} $*"; }
header() { echo -e "\n${BOLD}${CYAN}=== $* ===${NC}\n"; }

# ──────────────────────────────────────────────────────

check_root() {
    if [[ "$(id -u)" -ne 0 ]]; then
        error "This script must be run as root (sudo)"
        exit 1
    fi
}

detect_os() {
    header "Detecting OS"
    if [[ -f /etc/os-release ]]; then
        . /etc/os-release
        OS="$ID"
        OS_VERSION="$VERSION_ID"
    else
        error "Cannot detect OS. /etc/os-release not found."
        exit 1
    fi

    case "$OS" in
        ubuntu|debian) log "Detected: $OS $OS_VERSION" ;;
        *)
            error "Unsupported OS: $OS. Only Ubuntu/Debian are supported."
            exit 1
            ;;
    esac
}

check_wireguard() {
    header "Checking WireGuard"

    if command -v wg &>/dev/null; then
        log "WireGuard tools already installed ($(wg --version 2>&1 | head -1))"
    else
        warn "WireGuard not installed. Installing..."
        apt-get update -qq
        apt-get install -y wireguard wireguard-tools
        log "WireGuard installed successfully"
    fi

    if command -v qrencode &>/dev/null; then
        log "qrencode available (QR code support)"
    else
        apt-get install -y qrencode 2>/dev/null || log "qrencode optional (QR codes use fallback text)"
    fi
}

check_environment() {
    header "Checking Environment"

    if [[ "$(sysctl -n net.ipv4.ip_forward 2>/dev/null)" != "1" ]]; then
        warn "IP forwarding is not enabled. Enabling..."
        echo "net.ipv4.ip_forward = 1" > /etc/sysctl.d/99-wireguard.conf
        sysctl -p /etc/sysctl.d/99-wireguard.conf
        log "IP forwarding enabled"
    else
        log "IP forwarding: enabled"
    fi

    local wg_port="${1:-51820}"
    local mgmt_port="${2:-58880}"

    if ss -uln 2>/dev/null | grep -q ":$wg_port "; then
        warn "UDP port $wg_port is already in use"
    else
        log "UDP port $wg_port: available"
    fi

    if ss -tln 2>/dev/null | grep -q ":$mgmt_port "; then
        warn "TCP port $mgmt_port is already in use"
    else
        log "TCP port $mgmt_port: available"
    fi

    if lsmod 2>/dev/null | grep -q wireguard; then
        log "WireGuard kernel module: loaded"
    elif modprobe wireguard 2>/dev/null; then
        log "WireGuard kernel module: loaded manually"
    else
        warn "WireGuard kernel module not found — ensure wireguard-dkms is installed"
    fi

    if command -v go &>/dev/null; then
        local gov
        gov=$(go version 2>/dev/null | awk '{print $3}' || echo "unknown")
        log "Go: $gov"
    else
        warn "Go not installed — daemon cannot be built"
        warn "  Install: sudo apt install golang-go"
    fi

    if command -v python3 &>/dev/null; then
        log "python3: available"
    else
        warn "python3 not found — client scripts use python3 for JSON parsing"
        warn "  Install: sudo apt install python3"
    fi

    if command -v jq &>/dev/null; then
        log "jq: available (preferred JSON parser)"
    elif command -v python3 &>/dev/null; then
        log "jq: not installed (python3 fallback available)"
    else
        warn "Neither jq nor python3 available — JSON parsing will use basic shell fallback"
        warn "  Install jq for best results: sudo apt install jq"
    fi

    if command -v curl &>/dev/null; then log "curl: available"; else warn "curl not found"; fi
    if command -v openssl &>/dev/null; then log "openssl: available"; else warn "openssl not found"; fi

    if command -v iptables &>/dev/null; then
        log "iptables: available"
    else
        warn "iptables not found — WireGuard forwarding rules may not work"
    fi

    if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "active"; then
        warn "UFW is active — ensure it allows UDP $wg_port and TCP $mgmt_port"
    fi

    local avail_kb
    avail_kb=$(df /var/log --output=avail 2>/dev/null | tail -1 | tr -d ' ' || echo "0")
    if [[ "$avail_kb" -gt 102400 ]]; then
        log "Disk space (/var/log): $(($avail_kb / 1024)) MB"
    else
        warn "Low disk space on /var/log: $(($avail_kb / 1024)) MB (need 100+ MB for audit logs)"
    fi

    local mem_kb
    mem_kb=$(grep MemTotal /proc/meminfo 2>/dev/null | awk '{print $2}' || echo "0")
    if [[ "$mem_kb" -gt 65536 ]]; then
        log "Memory: $(($mem_kb / 1024)) MB"
    else
        warn "Low memory: $(($mem_kb / 1024)) MB (recommend 64+ MB free)"
    fi

    if timedatectl 2>/dev/null | grep -q "NTP service: active"; then
        log "NTP/time sync: active"
    else
        warn "NTP not active — WireGuard handshake timing depends on accurate clock"
    fi
}

detect_public_ip() {
    local ip
    for svc in "https://api.ipify.org" "https://ifconfig.me" "https://icanhazip.com"; do
        ip=$(curl -sSf --connect-timeout 3 "$svc" 2>/dev/null || true)
        if [[ -n "$ip" ]]; then
            echo "$ip"
            return
        fi
    done
    echo ""
}

collect_info() {
    header "Configuration"

    # ── Load existing config if available ──
    local existing_ip="" existing_wg_port=51820 existing_subnet="10.0.0.0/24"
    local existing_mgmt_port=58880 existing_dns="1.1.1.1,8.8.8.8"
    local skip_config=false

    if [[ -f "$CONFIG_FILE" ]]; then
        existing_ip=$(grep SERVER_PUBLIC_IP "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "")
        existing_wg_port=$(grep WG_PORT "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "51820")
        existing_subnet=$(grep WG_SUBNET "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "10.0.0.0/24")
        existing_mgmt_port=$(grep MGMT_LISTEN "$CONFIG_FILE" 2>/dev/null | sed 's/.*://' || echo "58880")
        existing_dns=$(grep DEFAULT_DNS "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "1.1.1.1,8.8.8.8")

        if [[ -n "$existing_ip" ]]; then
            warn "Existing configuration found:"
            info "  Public IP: $existing_ip"
            info "  WG Port:   $existing_wg_port"
            info "  Subnet:    $existing_subnet"
            info "  MGMT Port: $existing_mgmt_port"
            info "  DNS:       $existing_dns"
            echo ""
            read -p "$(echo -e "${BOLD}Use existing configuration? [Y/n]: ")" USE_EXISTING
            if [[ ! "$USE_EXISTING" =~ ^[Nn] ]]; then
                SERVER_PUBLIC_IP="$existing_ip"
                WG_PORT="$existing_wg_port"
                WG_SUBNET="$existing_subnet"
                MGMT_PORT="$existing_mgmt_port"
                DEFAULT_DNS="$existing_dns"
                skip_config=true
            fi
        fi
    fi

    if $skip_config; then
        return
    fi

    local detected_ip
    detected_ip=$(detect_public_ip)

    echo ""
    read -p "$(echo -e "${BOLD}Server Public IP${NC} [${detected_ip}]: ")" SERVER_PUBLIC_IP
    SERVER_PUBLIC_IP="${SERVER_PUBLIC_IP:-$detected_ip}"

    if [[ -z "$SERVER_PUBLIC_IP" ]]; then
        error "Server public IP is required"
        exit 1
    fi

    read -p "$(echo -e "${BOLD}WireGuard Port${NC} [51820]: ")" WG_PORT
    WG_PORT="${WG_PORT:-51820}"

    read -p "$(echo -e "${BOLD}VPN Subnet${NC} [10.0.0.0/24]: ")" WG_SUBNET
    WG_SUBNET="${WG_SUBNET:-10.0.0.0/24}"

    read -p "$(echo -e "${BOLD}Management API Port${NC} [58880]: ")" MGMT_PORT
    MGMT_PORT="${MGMT_PORT:-58880}"

    read -p "$(echo -e "${BOLD}Default Client DNS${NC} [1.1.1.1,8.8.8.8]: ")" DEFAULT_DNS
    DEFAULT_DNS="${DEFAULT_DNS:-1.1.1.1,8.8.8.8}"

    echo ""
    info "Configuration Summary:"
    info "  Public IP:     $SERVER_PUBLIC_IP"
    info "  WG Port:       $WG_PORT"
    info "  VPN Subnet:    $WG_SUBNET"
    info "  Management:    0.0.0.0:$MGMT_PORT"
    info "  Default DNS:   $DEFAULT_DNS"
    echo ""

    read -p "$(echo -e "${BOLD}Continue with these settings? [Y/n]: ")" CONFIRM
    if [[ "$CONFIRM" =~ ^[Nn] ]]; then
        error "Aborted by user"
        exit 1
    fi
}

init_wireguard_server() {
    header "Initializing WireGuard Server"

    local wg_conf="/etc/wireguard/wg0.conf"
    local server_private=""
    local server_public=""
    local server_address="${WG_SUBNET%.*}.1/24"
    local needs_init=true

    if [[ -f "$wg_conf" ]]; then
        warn "WireGuard config already exists at $wg_conf"
        server_private=$(grep -m1 "PrivateKey" "$wg_conf" 2>/dev/null | awk '{print $NF}' || echo "")
        if [[ -n "$server_private" ]]; then
            server_public=$(echo "$server_private" | wg pubkey 2>/dev/null || echo "")
        fi
        if [[ -z "$server_public" ]] && wg show wg0 &>/dev/null; then
            server_public=$(wg show wg0 public-key 2>/dev/null || echo "")
            server_private=$(wg show wg0 private-key 2>/dev/null || echo "")
        fi
        if [[ -n "$server_public" ]]; then
            needs_init=false
            log "Existing WireGuard server keys found — reusing."
        fi
    fi

    if $needs_init; then
        local peer_count
        peer_count=$(wg show wg0 peers 2>/dev/null | wc -l || echo "0")
        if [[ "$peer_count" -gt 0 ]]; then
            warn "WireGuard interface wg0 has $peer_count active peer(s) but server keys could not be read."
            warn "Re-initializing will REPLACE the keypair and all peer configs will be invalid."
            read -p "$(echo -e "${BOLD}Continue with re-initialization? This is DANGEROUS [y/N]: ")" REINIT_CONFIRM
            if [[ ! "$REINIT_CONFIRM" =~ ^[Yy] ]]; then
                error "Aborted. Try restoring from backup or contact support."
                exit 1
            fi
        fi
        cd "$PROJECT_DIR"

        # Backup existing data before overwriting
        if [[ -f "$PROJECT_DIR/server/peers.json" ]] && [[ -s "$PROJECT_DIR/server/peers.json" ]]; then
            warn "Backing up existing peers.json to peers.json.bak-$(date +%Y%m%d-%H%M%S)"
            cp "$PROJECT_DIR/server/peers.json" "$PROJECT_DIR/server/peers.json.bak-$(date +%Y%m%d-%H%M%S)"
        fi
        if [[ -f "$wg_conf" ]] && grep -q "^\[Peer\]" "$wg_conf" 2>/dev/null; then
            warn "Existing wg0.conf has peer sections — backing up before overwrite"
            cp "$wg_conf" "$wg_conf.bak-$(date +%Y%m%d-%H%M%S)"
        fi

        log "Generating server key pair..."
        server_private=$(wg genkey)
        server_public=$(echo "$server_private" | wg pubkey)

        # Preserve existing peer sections from old wg0.conf if any
        local old_peers=""
        if [[ -f "$wg_conf" ]]; then
            old_peers=$(awk '/^\[Peer\]/{p=1} p{print}' "$wg_conf" 2>/dev/null || echo "")
        fi

        log "Writing WireGuard config..."
        cat > "$wg_conf" << WGCONF
[Interface]
Address = $server_address
ListenPort = $WG_PORT
PrivateKey = $server_private
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT; iptables -A FORWARD -o wg0 -j ACCEPT
PostDown = iptables -D FORWARD -i wg0 -j ACCEPT; iptables -D FORWARD -o wg0 -j ACCEPT

WGCONF

        if [[ -n "$old_peers" ]]; then
            log "Preserving existing peer sections from old wg0.conf"
            echo "$old_peers" >> "$wg_conf"
        fi

        chmod 600 "$wg_conf"
        log "Config written to $wg_conf"

        systemctl enable wg-quick@wg0 --quiet
        systemctl restart wg-quick@wg0

        sleep 1
        if wg show wg0 &>/dev/null; then
            log "WireGuard interface wg0 is up"
        else
            error "Failed to start WireGuard interface wg0"
            exit 1
        fi

        local api_key
        api_key=$(openssl rand -hex 32 2>/dev/null || python3 -c "import secrets; print(secrets.token_hex(32))")

        if [[ -f "$PROJECT_DIR/server/peers.json" ]] && [[ -s "$PROJECT_DIR/server/peers.json" ]]; then
            warn "peers.json already exists — keeping existing data, daemon will reconcile"
            warn "Old backup saved as peers.json.bak-$(date +%Y%m%d-%H%M%S)"
        else
            log "Writing initial peers.json..."
            cat > "$PROJECT_DIR/server/peers.json" << PEERSJSON
{
  "server": {
    "public_key": "$server_public",
    "private_key": "$server_private",
    "endpoint": "$SERVER_PUBLIC_IP:$WG_PORT",
    "listen_port": $WG_PORT,
    "address": "$server_address",
    "subnet": "$WG_SUBNET"
  },
  "peers": {},
  "requests": {}
}
PEERSJSON
        fi
    fi
}

sync_config_env() {
    # Always run — syncs config.env with the latest template keys
    log "Syncing config.env..."

    local api_key
    if [[ -f "$CONFIG_FILE" ]]; then
        api_key=$(grep MGMT_API_KEY "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "")
    fi
    if [[ -z "$api_key" ]]; then
        api_key=$(openssl rand -hex 32 2>/dev/null || python3 -c "import secrets; print(secrets.token_hex(32))")
    fi

    local mgmt_listen="0.0.0.0:$MGMT_PORT"
    local server_address="${WG_SUBNET%.*}.1/24"

    cat > "$CONFIG_FILE" << CONFIGEOF
# WireGuard Management Layer Configuration
WG_INTERFACE=wg0
WG_PORT=$WG_PORT
WG_SUBNET=$WG_SUBNET
WG_SERVER_IP=$server_address
SERVER_PUBLIC_IP=$SERVER_PUBLIC_IP
MGMT_LISTEN=$mgmt_listen
MGMT_API_KEY=$api_key
DEFAULT_DNS=$DEFAULT_DNS
PEER_KEEPALIVE=25
PEERS_DB_PATH=$PROJECT_DIR/server/peers.json
WG_CONF_PATH=/etc/wireguard/wg0.conf
AUDIT_LOG_PATH=/var/log/wg-mgmt/audit.log
CONFIGEOF

    chmod 600 "$CONFIG_FILE"
    log "config.env synced"

    # ── Set up audit log ──
    local log_dir="/var/log/wg-mgmt"
    mkdir -p "$log_dir"
    chmod 750 "$log_dir"
    cat > /etc/logrotate.d/wg-mgmt << LOGROTATE
$log_dir/audit.log {
    daily
    rotate 30
    missingok
    notifempty
    compress
    delaycompress
    create 0640 root root
}
LOGROTATE
    log "Audit log configured: $log_dir/audit.log"
}

deploy_daemon() {
    header "Deploying Management Daemon"

    local bin_dst="/usr/local/bin/wg-mgmt-daemon"
    local daemon_was_running=false

    # ── 1. Check if daemon is currently running ──
    if systemctl is-active --quiet wg-mgmt 2>/dev/null; then
        daemon_was_running=true
        log "Stopping existing daemon for update..."
        systemctl stop wg-mgmt
    fi

    # ── 2. Determine if rebuild is needed ──
    local needs_rebuild=false

    if ! command -v go &>/dev/null; then
        error "Go is not installed. Please install golang-go: sudo apt install golang-go"
        exit 1
    fi

    local src_hash=""
    if [[ -d "$PROJECT_DIR/cmd" ]] && command -v git &>/dev/null; then
        cd "$PROJECT_DIR"
        src_hash=$(git log -1 --format=%H -- cmd/ internal/ 2>/dev/null || echo "")
    fi

    local installed_hash=""
    if [[ -f "$bin_dst" ]]; then
        installed_hash=$(cat "${bin_dst}.version" 2>/dev/null || echo "")
    fi

    if [[ -z "$src_hash" ]] || [[ -z "$installed_hash" ]] || [[ "$src_hash" != "$installed_hash" ]]; then
        needs_rebuild=true
    fi

    if $needs_rebuild; then
        cd "$PROJECT_DIR"
        log "Rebuilding from source (code has changed)..."
        go build -ldflags="-s -w" -o "$bin_dst" ./cmd/mgmt-daemon/
        chmod +x "$bin_dst"
        if [[ -n "$src_hash" ]]; then
            echo "$src_hash" > "${bin_dst}.version"
        fi
        log "Build complete"
    else
        log "Binary is up to date (commit $installed_hash)"
    fi

    # ── 2b. Build and deploy TUI ──
    local tui_dst="/usr/local/bin/wg-mgmt-tui"
    local tui_src_hash=""
    local tui_installed_hash=""

    if command -v git &>/dev/null; then
        cd "$PROJECT_DIR"
        tui_src_hash=$(git log -1 --format=%H -- cmd/mgmt-tui/ 2>/dev/null || echo "")
    fi
    if [[ -f "$tui_dst" ]]; then
        tui_installed_hash=$(cat "${tui_dst}.version" 2>/dev/null || echo "")
    fi

    if [[ -n "$tui_src_hash" ]] && { [[ -z "$tui_installed_hash" ]] || [[ "$tui_src_hash" != "$tui_installed_hash" ]]; }; then
        cd "$PROJECT_DIR"
        log "Rebuilding TUI..."
        go build -ldflags="-s -w" -o "$tui_dst" ./cmd/mgmt-tui/
        chmod +x "$tui_dst"
        echo "$tui_src_hash" > "${tui_dst}.version"
        log "TUI build complete"
    elif $needs_rebuild && [[ ! -f "$tui_dst" ]]; then
        cd "$PROJECT_DIR"
        log "Building TUI (first time)..."
        go build -ldflags="-s -w" -o "$tui_dst" ./cmd/mgmt-tui/
        chmod +x "$tui_dst"
        [[ -n "$tui_src_hash" ]] && echo "$tui_src_hash" > "${tui_dst}.version"
        log "TUI build complete"
    fi

    # ── 3. Write / update systemd unit ──
    log "Writing systemd service..."
    sed -e "s|__BIN_PATH__|$bin_dst|g" \
        -e "s|__CONFIG_PATH__|$CONFIG_FILE|g" \
        "$SCRIPT_DIR/wg-mgmt.service" > /etc/systemd/system/wg-mgmt.service

    systemctl daemon-reload
    systemctl enable wg-mgmt --quiet

    # ── 4. Start (or restart) daemon ──
    if $daemon_was_running; then
        log "Restarting daemon with updated binary..."
    else
        log "Starting daemon..."
    fi

    systemctl restart wg-mgmt

    sleep 2
    if systemctl is-active --quiet wg-mgmt; then
        log "Management daemon is running"
        if $daemon_was_running; then
            log "Update complete — existing WireGuard connections were not interrupted"
        fi
    else
        error "Management daemon failed to start"
        journalctl -u wg-mgmt --no-pager -n 20
        exit 1
    fi
}

print_summary() {
    header "Setup Complete"

    local mgmt_port="${MGMT_PORT:-58880}"
    local api_key
    api_key=$(grep MGMT_API_KEY "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 || echo "unknown")

    echo -e "${BOLD}${GREEN}WG-Manager is ready!${NC}"
    echo ""

    echo -e "  ${BOLD}Server Info${NC}"
    echo -e "    Server IP:    ${BOLD}${SERVER_PUBLIC_IP}${NC}"
    echo -e "    WG Port:      ${BOLD}${WG_PORT}${NC} (UDP)"
    echo -e "    MGMT Port:    ${BOLD}${mgmt_port}${NC} (TCP)"
    echo -e "    API Key:      ${BOLD}${api_key}${NC}"
    echo -e "    Default DNS:  ${BOLD}${DEFAULT_DNS}${NC}"
    echo ""

    echo -e "  ${BOLD}${CYAN}Client Connect${NC} ${YELLOW}-- copy a command to share${NC}"
    echo ""

    echo -e "    ${BOLD}Linux / macOS / WSL${NC}"
    echo -e "      Approval (no key):  curl -sSf http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect | sudo bash"
    echo -e "      Direct (trusted):   curl -sSf \"http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect?mode=direct&name=DEVICE\" | sudo bash"
    echo ""

    echo -e "    ${BOLD}Windows PowerShell${NC}"
    echo -e "      Approval:  Invoke-WebRequest http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect -OutFile join.ps1; .\\join.ps1"
    echo -e "      Direct:    Invoke-WebRequest \"http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect?mode=direct&name=MYPC\" -OutFile wg0.conf"
    echo ""

    echo -e "    ${BOLD}Windows CMD${NC}"
    echo -e "      Approval:  curl -X POST http://${SERVER_PUBLIC_IP}:${mgmt_port}/api/v1/request -H \"Content-Type: application/json\" -d \"{\\\"hostname\\\":\\\"MYPC\\\",\\\"dns\\\":\\\"1.1.1.1\\\"}\""
    echo -e "      Direct:    curl -o wg0.conf \"http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect?mode=direct&name=MYPC\""
    echo ""

    echo -e "    ${BOLD}Mobile QR${NC} (generate on server)"
    echo -e "      curl -s \"http://localhost:${mgmt_port}/connect?qrcode&mode=direct&name=phone1\" -o phone1.svg"
    echo -e "      Send phone1.svg to device -> WireGuard App -> Scan from QR code"
    echo ""

    echo -e "    ${BOLD}Browser${NC}:  http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect"
    echo ""

    echo -e "  ${BOLD}${CYAN}Server Management${NC}"
    echo -e "    ${BOLD}wg-mgmt-tui${NC}                              Interactive dashboard"
    echo -e "    ${BOLD}bash scripts/health-check.sh${NC}            System health check"
    echo -e "    ${BOLD}bash scripts/list-peers.sh${NC}             View all peers"
    echo -e "    ${BOLD}tail -f /var/log/wg-mgmt/audit.log${NC}     Live audit trail"
    echo ""

    echo -e "  ${BOLD}${YELLOW}Security${NC}"
    echo -e "    Firewall:       allow UDP ${WG_PORT} + TCP ${mgmt_port}"
    echo -e "    peers.json:     encrypted at rest (AES-256-GCM)"
    echo ""
}

# ──────────────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────────────

check_root
detect_os
check_wireguard
collect_info
check_environment "$WG_PORT" "$MGMT_PORT"
init_wireguard_server
sync_config_env
deploy_daemon
print_summary
