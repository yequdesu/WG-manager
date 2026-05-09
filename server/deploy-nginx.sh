#!/bin/bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
    exec sudo bash "$0" "$@"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"
NGINX_SITE="/etc/nginx/sites-available/wg-manager.conf"
NGINX_ENABLED="/etc/nginx/sites-enabled/wg-manager.conf"
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

log() { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*"; }
info() { echo -e "${CYAN}[i]${NC} $*"; }
header() { echo -e "\n${BOLD}${CYAN}=== $* ===${NC}\n"; }

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

check_dependencies() {
    if ! command -v apt-get &>/dev/null; then
        error "apt-get is required for nginx installation."
        exit 1
    fi

    if ! command -v nginx &>/dev/null; then
        header "Installing nginx"
        apt-get update -qq
        apt-get install -y nginx
        log "nginx installed"
    fi
}

load_config() {
    header "Loading configuration"

    if [[ ! -f "$CONFIG_FILE" ]]; then
        error "config.env not found at $CONFIG_FILE"
        exit 1
    fi

    MGMT_LISTEN=$(grep '^MGMT_LISTEN=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    MGMT_LISTEN="${MGMT_LISTEN:-127.0.0.1:58880}"
    MGMT_HOST="${MGMT_LISTEN%:*}"
    MGMT_PORT="${MGMT_LISTEN##*:}"

    if [[ -z "$MGMT_PORT" ]]; then
        MGMT_PORT="58880"
    fi

    if [[ "$MGMT_HOST" == "0.0.0.0" || -z "$MGMT_HOST" ]]; then
        MGMT_HOST="127.0.0.1"
    fi

    if [[ ! "$MGMT_PORT" =~ ^[0-9]+$ ]]; then
        error "Invalid MGMT_LISTEN port in config.env: $MGMT_LISTEN"
        exit 1
    fi

    log "Management backend: http://${MGMT_HOST}:${MGMT_PORT}"
}

detect_server_name() {
    local detected_ip=""

    if command -v curl &>/dev/null; then
        detected_ip=$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)
    fi

    if [[ -z "$detected_ip" ]]; then
        detected_ip=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -m1 -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || true)
    fi

    read -r -p "$(echo -e "${BOLD}Domain name or IP${NC} [${detected_ip:-127.0.0.1}]: ")" SERVER_NAME
    SERVER_NAME="${SERVER_NAME:-${detected_ip:-127.0.0.1}}"

    if [[ -z "$SERVER_NAME" ]]; then
        error "A domain name or IP is required."
        exit 1
    fi
}

update_config_value() {
  local config_file="$1"
  local key="$2"
  local new_value="$3"
  local existing_line current_line current_value
  local temp_file line line_has_newline had_content=0 last_was_newline=0 updated=0

  [[ -f "$config_file" ]] || return 1

  existing_line="$(grep -n "^${key}=" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  if [[ -z $existing_line ]]; then
    existing_line="$(grep -n "^${key} =" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  fi
  if [[ -z $existing_line ]]; then
    existing_line="$(grep -n "^${key}[[:space:]]*=" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  fi

  if [[ -n $existing_line ]]; then
    current_line="${existing_line#*:}"
    if [[ $current_line =~ ^(${key})([[:space:]]*)=([[:space:]]*)(.*)$ ]]; then
      current_value="${BASH_REMATCH[4]}"
      if [[ $current_value == "$new_value" ]]; then
        return 0
      fi
    fi
  fi

  temp_file="$(mktemp /tmp/wg-manager-config.XXXXXX)" || return 1

  while IFS= read -r line && line_has_newline=1 || { line_has_newline=0; [[ -n $line ]]; }; do
    had_content=1
    last_was_newline=$line_has_newline

    if [[ $line =~ ^(${key})([[:space:]]*)=([[:space:]]*)(.*)$ ]]; then
      printf '%s%s=%s%s' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "$new_value" >> "$temp_file"
      updated=1
    else
      printf '%s' "$line" >> "$temp_file"
    fi

    if [[ $last_was_newline -eq 1 ]]; then
      printf '\n' >> "$temp_file"
    fi

  done < "$config_file"

  if [[ $updated -eq 0 ]]; then
    if [[ $had_content -eq 1 && $last_was_newline -eq 0 ]]; then
      printf '\n' >> "$temp_file"
    fi
    printf '%s=%s\n' "$key" "$new_value" >> "$temp_file"
  fi

  mv "$temp_file" "$config_file"
}

sync_config_env() {
    local backup_path current new_value

    if [[ ! -f "$CONFIG_FILE" ]]; then
        error "config.env not found at $CONFIG_FILE"
        exit 1
    fi

    backup_path="${CONFIG_FILE}.bak-$(date +%Y%m%d-%H%M%S)"
    cp -a "$CONFIG_FILE" "$backup_path"

    if [[ "$SERVER_NAME" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        new_value=""
    else
        new_value="$SERVER_NAME"
    fi

    current=$(grep '^SERVER_HOST=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2-)
    if [[ "$current" == "$new_value" ]]; then
        return 0
    fi

    update_config_value "$CONFIG_FILE" "SERVER_HOST" "$new_value"
    log "Updated SERVER_HOST in config.env"
}

prepare_tls_paths() {
    CERT_FULLCHAIN="/etc/letsencrypt/live/${SERVER_NAME}/fullchain.pem"
    CERT_PRIVKEY="/etc/letsencrypt/live/${SERVER_NAME}/privkey.pem"

    if [[ -f "$CERT_FULLCHAIN" && -f "$CERT_PRIVKEY" ]]; then
        USE_TLS=true
        info "Detected Let's Encrypt certificates for ${SERVER_NAME}"
        if ! confirm "Use the detected TLS certificates" "Y"; then
            USE_TLS=false
        fi
    else
        USE_TLS=false
        warn "No matching TLS certificates found; generating an HTTP-only proxy config"
    fi
}

append_public_locations() {
    local backend="http://${MGMT_HOST}:${MGMT_PORT}"

    for route in /api/v1/health /api/v1/login /api/v1/logout /api/v1/redeem /bootstrap /connect; do
        cat >> "$TEMP_CONFIG" <<EOF
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

append_blocked_locations() {
    for route in /api/v1/peers /api/v1/invites /api/v1/users /api/v1/status; do
        cat >> "$TEMP_CONFIG" <<EOF
    location ^~ ${route} {
        return 403;
    }

EOF
    done
}

write_config() {
    TEMP_CONFIG="$(mktemp /tmp/wg-manager-nginx.XXXXXX)"

    if $USE_TLS; then
        cat > "$TEMP_CONFIG" <<EOF
server {
    listen 80;
    server_name ${SERVER_NAME};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl http2;
    server_name ${SERVER_NAME};

    ssl_certificate ${CERT_FULLCHAIN};
    ssl_certificate_key ${CERT_PRIVKEY};
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

EOF
        append_public_locations
        append_blocked_locations
        cat >> "$TEMP_CONFIG" <<'EOF'
}
EOF
    else
        cat > "$TEMP_CONFIG" <<EOF
server {
    listen 80;
    server_name ${SERVER_NAME};

EOF
        append_public_locations
        append_blocked_locations
        cat >> "$TEMP_CONFIG" <<'EOF'
}
EOF
    fi
}

backup_existing_config() {
    if [[ -e "$NGINX_SITE" ]]; then
        CONFIG_WAS_NEW=false
        local backup_path="${NGINX_SITE}.bak-$(date +%Y%m%d-%H%M%S)"
        warn "Existing nginx config found at $NGINX_SITE"
        if ! confirm "Overwrite it after creating a backup" "N"; then
            error "Aborted by user"
            exit 1
        fi
        cp -a "$NGINX_SITE" "$backup_path"
        BACKUP_PATH="$backup_path"
        log "Backup created: $backup_path"
    fi
}

install_config() {
    install -m 644 "$TEMP_CONFIG" "$NGINX_SITE"

    if [[ -L "$NGINX_ENABLED" ]]; then
        ENABLED_WAS_NEW=false
        local current_target
        current_target="$(readlink -f "$NGINX_ENABLED" 2>/dev/null || true)"
        if [[ "$current_target" != "$NGINX_SITE" ]]; then
            warn "Existing enabled site points somewhere else: $current_target"
            if ! confirm "Replace the enabled nginx site symlink" "N"; then
                error "Aborted by user"
                exit 1
            fi
            local enabled_backup="${NGINX_ENABLED}.bak-$(date +%Y%m%d-%H%M%S)"
            cp -a "$NGINX_ENABLED" "$enabled_backup"
            ENABLED_BACKUP_PATH="$enabled_backup"
            rm -f "$NGINX_ENABLED"
            ln -s "$NGINX_SITE" "$NGINX_ENABLED"
        fi
    elif [[ -e "$NGINX_ENABLED" ]]; then
        ENABLED_WAS_NEW=false
        warn "A non-symlink file already exists at $NGINX_ENABLED"
        if ! confirm "Replace it with a symlink to the new site" "N"; then
            error "Aborted by user"
            exit 1
        fi
        local enabled_backup="${NGINX_ENABLED}.bak-$(date +%Y%m%d-%H%M%S)"
        cp -a "$NGINX_ENABLED" "$enabled_backup"
        ENABLED_BACKUP_PATH="$enabled_backup"
        rm -f "$NGINX_ENABLED"
        ln -s "$NGINX_SITE" "$NGINX_ENABLED"
    else
        ln -s "$NGINX_SITE" "$NGINX_ENABLED"
    fi
}

rollback_installation() {
    if [[ -n "$BACKUP_PATH" && -f "$BACKUP_PATH" ]]; then
        cp -a "$BACKUP_PATH" "$NGINX_SITE"
    elif [[ "$CONFIG_WAS_NEW" == true ]]; then
        rm -f "$NGINX_SITE"
    fi

    if [[ -n "$ENABLED_BACKUP_PATH" && -f "$ENABLED_BACKUP_PATH" ]]; then
        rm -f "$NGINX_ENABLED"
        cp -a "$ENABLED_BACKUP_PATH" "$NGINX_ENABLED"
    elif [[ "$ENABLED_WAS_NEW" == true ]]; then
        rm -f "$NGINX_ENABLED"
    fi
}

validate_config() {
    header "Validating nginx"
    if nginx -t; then
        log "nginx configuration is valid"
    else
        rollback_installation
        error "nginx validation failed"
        exit 1
    fi
}

reload_nginx() {
    if ! confirm "Reload nginx now" "Y"; then
        warn "Skipped nginx reload"
        return
    fi

    if systemctl is-active --quiet nginx 2>/dev/null; then
        systemctl reload nginx
    else
        systemctl enable --now nginx
    fi

    log "nginx reloaded"
}

main() {
    check_dependencies
    load_config
    detect_server_name
    sync_config_env
    prepare_tls_paths

    header "Deployment Summary"
    info "Site file:     $NGINX_SITE"
    info "Enabled site:  $NGINX_ENABLED"
    info "Backend:       http://${MGMT_HOST}:${MGMT_PORT}"
    info "Server name:   $SERVER_NAME"
    info "Mode:          $([ "$USE_TLS" = true ] && echo "HTTPS" || echo "HTTP-only")"
    info "Public routes:  /api/v1/health, /api/v1/login, /api/v1/logout, /api/v1/redeem, /bootstrap, /connect"
    info "Blocked routes: /api/v1/peers, /api/v1/invites, /api/v1/users, /api/v1/status"
    echo ""

    if ! confirm "Continue?" "Y"; then
        error "Aborted by user"
        exit 1
    fi

    backup_existing_config
    write_config
    install_config
    validate_config
    reload_nginx
    if systemctl is-active --quiet wg-mgmt 2>/dev/null; then
        systemctl kill -s HUP wg-mgmt 2>/dev/null || true
        log "Notified wg-mgmt daemon to reload configuration"
    fi
}

main "$@"
