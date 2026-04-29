#!/bin/bash
set -euo pipefail

# WireGuard Management Layer - Server Setup Script
# Run as root on Ubuntu/Debian server

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

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

    if ss -uln | grep -q ":$wg_port "; then
        warn "UDP port $wg_port is already in use"
    else
        log "UDP port $wg_port: available"
    fi

    if ss -tln | grep -q ":$mgmt_port "; then
        warn "TCP port $mgmt_port is already in use"
    else
        log "TCP port $mgmt_port: available"
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
        if [[ -f "$PROJECT_DIR/server/peers.json" ]]; then
            log "Found existing peers.json, WireGuard already initialized."
            server_public=$(python3 -c "import json; print(json.load(open('$PROJECT_DIR/server/peers.json'))['server']['public_key'])" 2>/dev/null || echo "")
            server_private=$(python3 -c "import json; print(json.load(open('$PROJECT_DIR/server/peers.json'))['server']['private_key'])" 2>/dev/null || echo "")
            if [[ -n "$server_public" ]]; then
                needs_init=false
            fi
        fi
    fi

    if $needs_init; then
        cd "$PROJECT_DIR"

        log "Generating server key pair..."
        server_private=$(wg genkey)
        server_public=$(echo "$server_private" | wg pubkey)

        log "Writing WireGuard config..."
        cat > "$wg_conf" << WGCONF
[Interface]
Address = $server_address
ListenPort = $WG_PORT
PrivateKey = $server_private
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT; iptables -A FORWARD -o wg0 -j ACCEPT
PostDown = iptables -D FORWARD -i wg0 -j ACCEPT; iptables -D FORWARD -o wg0 -j ACCEPT

WGCONF

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

        log "Writing peers.json..."
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
  "requests": {},
  "next_ip_suffix": 2
}
PEERSJSON
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
    cat > /etc/systemd/system/wg-mgmt.service << SYSTEMD
[Unit]
Description=WireGuard Management Daemon
After=network-online.target wg-quick@wg0.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=$bin_dst --config=$CONFIG_FILE
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SYSTEMD

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

    echo -e "${BOLD}${GREEN}WireGuard Management Layer is ready!${NC}"
    echo ""
    echo -e "  ${BOLD}Server IP:${NC}     $SERVER_PUBLIC_IP"
    echo -e "  ${BOLD}WG Port:${NC}       $WG_PORT"
    echo -e "  ${BOLD}MGMT Port:${NC}     $mgmt_port"
    echo -e "  ${BOLD}API Key:${NC}       $api_key"
    echo ""
    echo -e "  ${BOLD}${CYAN}Connect (all platforms, approval mode):${NC}"
    echo -e "    ${BOLD}curl -sSf http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect | sudo bash${NC}"
    echo ""
    echo -e "  ${BOLD}${CYAN}Direct (trusted users, with API key):${NC}"
    echo -e "    ${BOLD}curl -sSf \"http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect?mode=direct&name=DEVICE\" | sudo bash${NC}"
    echo ""
    echo -e "  ${BOLD}${CYAN}Windows / Browser:${NC}"
    echo -e "    ${BOLD}Open http://${SERVER_PUBLIC_IP}:${mgmt_port}/connect${NC}"
    echo ""
    echo -e "  ${YELLOW}Admin commands (run on server):${NC}"
    echo -e "    ${BOLD}wg-mgmt-tui${NC}                                          # TUI manager"
    echo -e "    ${BOLD}curl -s http://127.0.0.1:${mgmt_port}/api/v1/peers \\${NC}"
    echo -e "         ${BOLD}-H 'Authorization: Bearer ${api_key}' | python3 -m json.tool${NC}"
    echo -e "    ${BOLD}tail -f /var/log/wg-mgmt/audit.log${NC}                   # Audit log"
    echo -e "         -H 'Authorization: Bearer ${api_key}'"
    echo ""
    echo -e "  ${YELLOW}Important:${NC} Ensure your cloud firewall/security group allows:"
    echo -e "    - UDP port ${WG_PORT}  (WireGuard)"
    echo -e "    - TCP port ${mgmt_port}  (Management API for client registration)"
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
