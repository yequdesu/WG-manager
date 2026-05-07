#!/bin/bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
    exec sudo bash "$0" "$@"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

PROXY_MODE="nginx"
CERT_EMAIL=""

CONFIG_PATH=""
ENABLED_PATH=""
BACKUP_PATH=""
ENABLED_BACKUP_PATH=""
CONFIG_WAS_NEW=true
ENABLED_WAS_NEW=true

export DEBIAN_FRONTEND=noninteractive

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()   { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*" >&2; }
info()  { echo -e "${CYAN}[i]${NC} $*"; }
header(){ echo -e "\n${BOLD}${CYAN}=== $* ===${NC}\n"; }

confirm() {
    local prompt="$1"
    local default="${2:-Y}"
    local reply

    if [[ "$default" =~ ^[Yy]$ ]]; then
        read -r -p "$(echo -e "${BOLD}${prompt}${NC} [Y/n]: ")" reply
        reply="${reply:-Y}"
    else
        read -r -p "$(echo -e "${BOLD}${prompt}${NC} [y/N]: ")" reply
        reply="${reply:-N}"
    fi

    [[ "$reply" =~ ^[Yy]$ ]]
}

usage() {
    cat <<EOF
Usage: $0 [--nginx | --caddy] [-h | --help]

Deploy a reverse proxy for WG-Manager public HTTPS exposure.

Options:
  --nginx    Use nginx as the reverse proxy (default)
  --caddy    Use Caddy as the reverse proxy
  -h, --help Show this help

Both modes:
  * Load config from config.env (MGMT_LISTEN, SERVER_HOST, SERVER_PUBLIC_IP, WG_SUBNET)
  * Detect domain or IP; HTTPS if domain resolves, HTTP-only fallback otherwise
  * Block admin routes at the proxy level (403 for peers, invites, users, status)
  * Back up existing config before overwriting (timestamped)
  * Validate generated config before reloading
  * Confirm before destructive operations
EOF
}

parse_args() {
    local got_mode=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --nginx)
                if $got_mode && [[ "$PROXY_MODE" != "nginx" ]]; then
                    error "--nginx and --caddy are mutually exclusive"
                    exit 1
                fi
                PROXY_MODE="nginx"
                got_mode=true
                shift
                ;;
            --caddy)
                if $got_mode && [[ "$PROXY_MODE" != "caddy" ]]; then
                    error "--nginx and --caddy are mutually exclusive"
                    exit 1
                fi
                PROXY_MODE="caddy"
                got_mode=true
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

check_dependencies() {
    if ! command -v apt-get &>/dev/null; then
        error "apt-get is required for proxy installation."
        exit 1
    fi

    if [[ "$PROXY_MODE" == "nginx" ]]; then
        CONFIG_PATH="/etc/nginx/sites-available/wg-manager.conf"
        ENABLED_PATH="/etc/nginx/sites-enabled/wg-manager.conf"

        if ! command -v nginx &>/dev/null; then
            header "Installing nginx"
            apt-get update -qq
            apt-get install -y nginx
            log "nginx installed"
        fi

        if ! command -v certbot &>/dev/null; then
            info "Installing certbot"
            apt-get update -qq
            apt-get install -y certbot
            log "certbot installed"
        fi
    fi

    if [[ "$PROXY_MODE" == "caddy" ]]; then
        CONFIG_PATH="/etc/caddy/Caddyfile"

        if ! command -v caddy &>/dev/null; then
            header "Installing Caddy"
            apt-get update -qq
            apt-get install -y debian-keyring debian-archive-keyring apt-transport-https 2>/dev/null || true
            if ! apt-get install -y caddy 2>/dev/null; then
                info "Adding official Caddy repository"
                curl -fsSL -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg \
                    https://dl.cloudsmith.io/public/caddy/stable/gpg.key 2>/dev/null || true
                if [[ -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg ]]; then
                    echo "deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main" \
                        | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
                    apt-get update -qq
                    apt-get install -y caddy
                else
                    error "Could not install Caddy. Check network or install manually: https://caddyserver.com/docs/install"
                    exit 1
                fi
            fi
            log "Caddy installed"
        fi
    fi
}

load_config() {
    header "Loading configuration"

    if [[ ! -f "$CONFIG_FILE" ]]; then
        error "config.env not found at $CONFIG_FILE"
        exit 1
    fi

    local raw_listen
    raw_listen=$(grep '^MGMT_LISTEN=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    raw_listen="${raw_listen:-127.0.0.1:58880}"
    MGMT_HOST="${raw_listen%:*}"
    MGMT_PORT="${raw_listen##*:}"

    if [[ -z "$MGMT_PORT" ]]; then
        MGMT_PORT="58880"
    fi

    if [[ "$MGMT_HOST" == "0.0.0.0" || -z "$MGMT_HOST" ]]; then
        MGMT_HOST="127.0.0.1"
    fi

    if [[ ! "$MGMT_PORT" =~ ^[0-9]+$ ]]; then
        error "Invalid MGMT_LISTEN port in config.env: $raw_listen"
        exit 1
    fi

    log "Management backend: http://${MGMT_HOST}:${MGMT_PORT}"

    CONFIG_SERVER_HOST=$(grep '^SERVER_HOST=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    CONFIG_SERVER_PUBLIC_IP=$(grep '^SERVER_PUBLIC_IP=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    WG_SUBNET=$(grep '^WG_SUBNET=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    WG_SUBNET="${WG_SUBNET:-10.0.0.0/24}"
}

detect_server_name() {
    local detected_name=""

    if [[ -n "$CONFIG_SERVER_HOST" ]]; then
        detected_name="$CONFIG_SERVER_HOST"
    fi

    if [[ -z "$detected_name" && -n "$CONFIG_SERVER_PUBLIC_IP" ]]; then
        detected_name="$CONFIG_SERVER_PUBLIC_IP"
    fi

    if [[ -z "$detected_name" ]]; then
        if command -v curl &>/dev/null; then
            detected_name=$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)
        fi
        if [[ -z "$detected_name" ]]; then
            detected_name=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -m1 -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || true)
        fi
    fi

    read -r -p "$(echo -e "${BOLD}Domain name or IP${NC} [${detected_name:-127.0.0.1}]: ")" SERVER_NAME
    SERVER_NAME="${SERVER_NAME:-${detected_name:-127.0.0.1}}"

    if [[ -z "$SERVER_NAME" ]]; then
        error "A domain name or IP is required."
        exit 1
    fi
}

is_ip_address() {
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

resolve_domain() {
    local domain="$1"
    local resolved=""

    if command -v dig &>/dev/null; then
        resolved=$(dig +short "$domain" 2>/dev/null | grep -m1 -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
    elif command -v host &>/dev/null; then
        resolved=$(host "$domain" 2>/dev/null | grep -m1 'has address' | awk '{print $NF}' || true)
    elif command -v nslookup &>/dev/null; then
        resolved=$(nslookup "$domain" 2>/dev/null | grep -m1 'Address:' | tail -1 | awk '{print $2}' || true)
    elif command -v getent &>/dev/null; then
        resolved=$(getent ahosts "$domain" 2>/dev/null | grep -m1 RAW | awk '{print $1}' || true)
    fi

    if [[ -n "$resolved" ]]; then
        log "Domain $domain resolves to $resolved"
        return 0
    fi
    return 1
}

detect_proxy_mode() {
    USE_TLS=false

    if is_ip_address "$SERVER_NAME"; then
        warn "Detected an IP address — HTTPS requires a domain name"
        echo ""
        echo -e "  ${BOLD}No domain detected — HTTPS unavailable; bootstrap URLs will use HTTP${NC}"
        echo ""
        return
    fi

    info "Checking if $SERVER_NAME resolves via DNS..."
    if resolve_domain "$SERVER_NAME"; then
        USE_TLS=true
        log "Domain resolves — HTTPS will be available"
    else
        warn "$SERVER_NAME does not resolve in DNS"
        if ! confirm "Continue with HTTP-only fallback" "N"; then
            error "Aborted by user"
            exit 1
        fi
        echo ""
        echo -e "  ${BOLD}No domain detected — HTTPS unavailable; bootstrap URLs will use HTTP${NC}"
        echo ""
    fi
}

obtain_certs() {
    if [[ ! $USE_TLS ]]; then
        return 0
    fi

    if [[ "$PROXY_MODE" == "caddy" ]]; then
        info "Caddy handles TLS certificates automatically via Let's Encrypt"
        return 0
    fi

    local cert_fullchain="/etc/letsencrypt/live/${SERVER_NAME}/fullchain.pem"
    local cert_privkey="/etc/letsencrypt/live/${SERVER_NAME}/privkey.pem"

    if [[ -f "$cert_fullchain" && -f "$cert_privkey" ]]; then
        info "Existing Let's Encrypt certificates found for ${SERVER_NAME}"
        if confirm "Use the existing certificates" "Y"; then
            log "Using existing certificates"
            return 0
        fi
    fi

    if [[ -z "$CERT_EMAIL" ]]; then
        read -r -p "$(echo -e "${BOLD}Email for Let's Encrypt notifications${NC}: ")" CERT_EMAIL
        if [[ -z "$CERT_EMAIL" ]]; then
            warn "No email provided — using admin@${SERVER_NAME}"
            CERT_EMAIL="admin@${SERVER_NAME}"
        fi
    fi

    header "Obtaining Let's Encrypt certificate"

    local was_nginx_running=false
    if systemctl is-active --quiet nginx 2>/dev/null; then
        was_nginx_running=true
        info "Stopping nginx temporarily for standalone HTTP challenge"
        systemctl stop nginx
    fi

    if certbot certonly --standalone --preferred-challenges http \
        -d "$SERVER_NAME" \
        --non-interactive --agree-tos \
        --email "$CERT_EMAIL" \
        --quiet 2>&1 | tail -5; then
        log "Certificate obtained for ${SERVER_NAME}"
    else
        error "certbot failed to obtain a certificate"
        if $was_nginx_running; then
            systemctl start nginx 2>/dev/null || true
        fi
        exit 1
    fi

    if $was_nginx_running; then
        systemctl start nginx 2>/dev/null || true
    fi
}

generate_nginx_config() {
    local temp_config
    temp_config="$(mktemp /tmp/wg-manager-nginx.XXXXXX)"

    local backend="http://${MGMT_HOST}:${MGMT_PORT}"

    if $USE_TLS; then
        cat > "$temp_config" <<EOF
# WG-Manager reverse proxy (generated by deploy-proxy.sh)
server {
    listen 80;
    server_name ${SERVER_NAME};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl http2;
    server_name ${SERVER_NAME};

    ssl_certificate     /etc/letsencrypt/live/${SERVER_NAME}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${SERVER_NAME}/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

EOF
        append_nginx_public_locations "$temp_config" "$backend"
        append_nginx_blocked_locations "$temp_config"
        cat >> "$temp_config" <<'EOF'
}
EOF
    else
        cat > "$temp_config" <<EOF
# WG-Manager reverse proxy (generated by deploy-proxy.sh)
server {
    listen 80;
    server_name ${SERVER_NAME};

EOF
        append_nginx_public_locations "$temp_config" "$backend"
        append_nginx_blocked_locations "$temp_config"
        cat >> "$temp_config" <<'EOF'
}
EOF
    fi

    echo "$temp_config"
}

append_nginx_public_locations() {
    local cfg_file="$1"
    local backend="$2"

    for route in /api/v1/health /api/v1/login /api/v1/logout /api/v1/redeem /bootstrap /connect; do
        cat >> "$cfg_file" <<EOF
    location = ${route} {
        proxy_pass ${backend};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

EOF
    done
}

append_nginx_blocked_locations() {
    local cfg_file="$1"

    for route in /api/v1/peers /api/v1/invites /api/v1/users /api/v1/status; do
        cat >> "$cfg_file" <<EOF
    location ^~ ${route} {
        return 403;
    }

EOF
    done
}

generate_caddy_config() {
    local temp_config
    temp_config="$(mktemp /tmp/wg-manager-caddy.XXXXXX)"

    local backend="${MGMT_HOST}:${MGMT_PORT}"

    if $USE_TLS; then
        cat > "$temp_config" <<EOF
# WG-Manager reverse proxy (generated by deploy-proxy.sh)
${SERVER_NAME} {
EOF
    else
        cat > "$temp_config" <<EOF
# WG-Manager reverse proxy (generated by deploy-proxy.sh)
:80 {
EOF
    fi

    for route in /api/v1/health /api/v1/login /api/v1/logout /api/v1/redeem /bootstrap /connect; do
        cat >> "$temp_config" <<EOF
    reverse_proxy ${route} ${backend}
EOF
    done

    for route in /api/v1/peers /api/v1/invites /api/v1/users /api/v1/status; do
        cat >> "$temp_config" <<EOF
    respond ${route} 403
EOF
    done

    cat >> "$temp_config" <<'EOF'
}
EOF

    echo "$temp_config"
}

backup_existing_config() {
    if [[ -e "$CONFIG_PATH" ]]; then
        CONFIG_WAS_NEW=false
        local backup_target="${CONFIG_PATH}.bak-$(date +%Y%m%d-%H%M%S)"
        warn "Existing proxy config found at $CONFIG_PATH"
        if ! confirm "Overwrite it after creating a backup" "N"; then
            error "Aborted by user"
            exit 1
        fi
        cp -a "$CONFIG_PATH" "$backup_target"
        BACKUP_PATH="$backup_target"
        log "Backup created: $backup_target"
    fi

    if [[ "$PROXY_MODE" == "nginx" ]]; then
        if [[ -e "$ENABLED_PATH" ]]; then
            ENABLED_WAS_NEW=false
            if ! confirm "Replace the enabled nginx site" "N"; then
                error "Aborted by user"
                exit 1
            fi
            local enabled_backup="${ENABLED_PATH}.bak-$(date +%Y%m%d-%H%M%S)"
            cp -a "$ENABLED_PATH" "$enabled_backup"
            ENABLED_BACKUP_PATH="$enabled_backup"
            rm -f "$ENABLED_PATH"
        fi
    fi
}

install_config() {
    local temp_config="$1"

    if [[ "$PROXY_MODE" == "nginx" ]]; then
        install -m 644 "$temp_config" "$CONFIG_PATH"
        ln -sf "$CONFIG_PATH" "$ENABLED_PATH"
        log "nginx config installed: $CONFIG_PATH -> $ENABLED_PATH"
    fi

    if [[ "$PROXY_MODE" == "caddy" ]]; then
        install -m 644 "$temp_config" "$CONFIG_PATH"
        log "Caddy config installed: $CONFIG_PATH"
    fi
}

rollback_installation() {
    if [[ -n "$BACKUP_PATH" && -f "$BACKUP_PATH" ]]; then
        cp -a "$BACKUP_PATH" "$CONFIG_PATH"
    elif [[ "$CONFIG_WAS_NEW" == true ]]; then
        rm -f "$CONFIG_PATH"
    fi

    if [[ "$PROXY_MODE" == "nginx" ]]; then
        if [[ -n "$ENABLED_BACKUP_PATH" && -f "$ENABLED_BACKUP_PATH" ]]; then
            rm -f "$ENABLED_PATH"
            cp -a "$ENABLED_BACKUP_PATH" "$ENABLED_PATH"
        elif [[ "$ENABLED_WAS_NEW" == true ]]; then
            rm -f "$ENABLED_PATH"
        fi
    fi
}

validate_config() {
    header "Validating $PROXY_MODE configuration"

    if [[ "$PROXY_MODE" == "nginx" ]]; then
        if nginx -t; then
            log "nginx configuration is valid"
        else
            rollback_installation
            error "nginx validation failed — config rolled back"
            exit 1
        fi
    fi

    if [[ "$PROXY_MODE" == "caddy" ]]; then
        if caddy validate --config "$CONFIG_PATH" 2>&1; then
            log "Caddy configuration is valid"
        else
            rollback_installation
            error "Caddy validation failed — config rolled back"
            exit 1
        fi
    fi
}

reload_proxy() {
    if ! confirm "Reload $PROXY_MODE now" "Y"; then
        warn "Skipped $PROXY_MODE reload"
        return
    fi

    local svc_name="$PROXY_MODE"

    if systemctl is-active --quiet "$svc_name" 2>/dev/null; then
        systemctl reload "$svc_name"
    else
        systemctl enable --now "$svc_name"
    fi

    log "$PROXY_MODE reloaded"
}

main() {
    parse_args "$@"
    check_dependencies
    load_config
    detect_server_name
    detect_proxy_mode

    header "Deployment Summary"
    info "Proxy:        $PROXY_MODE"
    info "Config path:  $CONFIG_PATH"
    info "Backend:      http://${MGMT_HOST}:${MGMT_PORT}"
    info "Server name:  $SERVER_NAME"
    info "Mode:         $([ "$USE_TLS" = true ] && echo "HTTPS" || echo "HTTP-only")"

    if $USE_TLS; then
        if [[ "$PROXY_MODE" == "nginx" ]]; then
            info "Cert:         /etc/letsencrypt/live/${SERVER_NAME}/"
        else
            info "Cert:         Caddy auto-TLS (Let's Encrypt)"
        fi
    fi

    info "Public routes:  /api/v1/health, /api/v1/login, /api/v1/logout, /api/v1/redeem, /bootstrap, /connect"
    info "Blocked routes: /api/v1/peers, /api/v1/invites, /api/v1/users, /api/v1/status"
    echo ""

    if ! confirm "Continue?" "Y"; then
        error "Aborted by user"
        exit 1
    fi

    obtain_certs

    local temp_config
    if [[ "$PROXY_MODE" == "nginx" ]]; then
        temp_config="$(generate_nginx_config)"
    else
        temp_config="$(generate_caddy_config)"
    fi

    backup_existing_config
    install_config "$temp_config"
    validate_config
    reload_proxy

    rm -f "$temp_config"

    header "Deployment Complete"
    if $USE_TLS; then
        log "HTTPS proxy is active at https://${SERVER_NAME}"
    else
        log "HTTP proxy is active at http://${SERVER_NAME}"
    fi
    log "Backend:  http://${MGMT_HOST}:${MGMT_PORT}"
    log "Config:   $CONFIG_PATH"
    [[ -n "$BACKUP_PATH" ]] && log "Backup:   $BACKUP_PATH"
    echo ""
}

main "$@"
