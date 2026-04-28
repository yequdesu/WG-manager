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

    if [[ -f "$wg_conf" ]]; then
        warn "WireGuard config already exists at $wg_conf"
        if [[ -f "$PROJECT_DIR/server/peers.json" ]]; then
            log "Found existing peers.json, importing..."
            return
        fi
    fi

    cd "$PROJECT_DIR"

    log "Generating server key pair..."
    local server_private
    server_private=$(wg genkey)
    local server_public
    server_public=$(echo "$server_private" | wg pubkey)

    local server_address="${WG_SUBNET%.*}.1/24"

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
  "next_ip_suffix": 2
}
PEERSJSON

    log "Writing config.env..."
    local mgmt_listen="0.0.0.0:$MGMT_PORT"
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
CLIENT_SCRIPT_TEMPLATE=$PROJECT_DIR/client/connect.sh
CONFIGEOF

    log "Configuration saved"
}

deploy_daemon() {
    header "Deploying Management Daemon"

    local bin_dst="/usr/local/bin/wg-mgmt-daemon"

    if [[ -f "$PROJECT_DIR/bin/wg-mgmt-daemon" ]]; then
        log "Using pre-compiled binary"
        cp "$PROJECT_DIR/bin/wg-mgmt-daemon" "$bin_dst"
        chmod +x "$bin_dst"
    elif [[ -f "$PROJECT_DIR/wg-mgmt-daemon" ]]; then
        log "Using pre-compiled binary"
        cp "$PROJECT_DIR/wg-mgmt-daemon" "$bin_dst"
        chmod +x "$bin_dst"
    else
        warn "No pre-compiled binary found. Attempting to build..."
        if command -v go &>/dev/null; then
            cd "$PROJECT_DIR"
            go build -ldflags="-s -w" -o "$bin_dst" ./cmd/mgmt-daemon/
            chmod +x "$bin_dst"
            log "Built successfully"
        else
            error "Go is not installed and no pre-compiled binary found."
            error "Please compile on your dev machine with: make build"
            error "Then copy bin/wg-mgmt-daemon to this server and re-run."
            exit 1
        fi
    fi

    log "Creating systemd service..."
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

ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
ReadWritePaths=$PROJECT_DIR/server /etc/wireguard
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
SYSTEMD

    systemctl daemon-reload
    systemctl enable wg-mgmt --quiet
    systemctl restart wg-mgmt

    sleep 2
    if systemctl is-active --quiet wg-mgmt; then
        log "Management daemon is running"
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
    echo -e "  ${BOLD}${CYAN}Clients join by running:${NC}"
    echo -e "  ${BOLD}curl -sSf http://${SERVER_PUBLIC_IP}:${mgmt_port}/api/v1/client-script | sudo bash${NC}"
    echo ""
    echo -e "  ${YELLOW}Admin commands (run on server):${NC}"
    echo -e "    curl -s http://127.0.0.1:${mgmt_port}/api/v1/peers \\"
    echo -e "         -H 'Authorization: Bearer ${api_key}' | python3 -m json.tool"
    echo -e "    curl -s -X DELETE http://127.0.0.1:${mgmt_port}/api/v1/peers/<name> \\"
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
deploy_daemon
print_summary
