#!/bin/bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
    exec sudo bash "$0" "$@"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"

CERT_DIR=""
DOMAIN=""
SERVER_HOST=""
SOURCE_CERT=""
SOURCE_KEY=""
TARGET_DIR=""
TARGET_CERT=""
TARGET_KEY=""
CERT_BACKUP=""
KEY_BACKUP=""
CERT_BACKUP_CREATED=false
KEY_BACKUP_CREATED=false
CERT_INSTALLED=false
KEY_INSTALLED=false
DEPLOY_PROXY_NEEDED=false
DEPLOY_PROXY_RAN=false
BACKUP_SUFFIX="$(date +%Y%m%d-%H%M%S)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log() { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*" >&2; }
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

usage() {
    cat <<EOF
Usage: sudo bash server/install-cert.sh <cert-dir> [domain]

Install Aliyun SSL certificate files into /etc/letsencrypt/live/<domain>/,
validate them, and safely reload nginx with rollback.
EOF
}

load_server_host() {
    if [[ -f "$CONFIG_FILE" ]]; then
        SERVER_HOST=$(grep '^SERVER_HOST=' "$CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d ' ' || true)
    fi
}

prompt_domain() {
    local default_domain="${SERVER_HOST:-}"

    if [[ -n "$default_domain" ]]; then
        read -r -p "$(echo -e "${BOLD}Domain${NC} [${default_domain}]: ")" DOMAIN
        DOMAIN="${DOMAIN:-$default_domain}"
    else
        read -r -p "$(echo -e "${BOLD}Domain${NC}: ")" DOMAIN
    fi

    if [[ -z "$DOMAIN" ]]; then
        error "Domain is required."
        exit 1
    fi
}

normalize_path() {
    local path="$1"

    if command -v realpath &>/dev/null; then
        realpath "$path"
    elif command -v readlink &>/dev/null; then
        readlink -f "$path" 2>/dev/null || printf '%s\n' "$path"
    else
        printf '%s\n' "$path"
    fi
}

join_paths() {
    local -a items=("$@")
    local output=""
    local item

    for item in "${items[@]}"; do
        output+="${output:+, }$item"
    done

    printf '%s\n' "$output"
}

resolve_certificate_sources() {
    local preferred_cert="$CERT_DIR/$DOMAIN.pem"
    local preferred_key="$CERT_DIR/$DOMAIN.key"
    local -a cert_candidates=()
    local -a key_candidates=()

    shopt -s nullglob
    cert_candidates=("$CERT_DIR"/*.pem "$CERT_DIR"/*.crt "$CERT_DIR"/*.cer)
    key_candidates=("$CERT_DIR"/*.key)
    shopt -u nullglob

    if [[ -f "$preferred_cert" && -f "$preferred_key" ]]; then
        SOURCE_CERT="$preferred_cert"
        SOURCE_KEY="$preferred_key"
        return 0
    fi

    if [[ ${#cert_candidates[@]} -ne 1 || ${#key_candidates[@]} -ne 1 ]]; then
        error "Could not unambiguously select certificate files in: $CERT_DIR"
        if [[ ${#cert_candidates[@]} -eq 0 ]]; then
            error "No *.pem, *.crt, or *.cer file found."
        else
            error "Certificate candidates: $(join_paths "${cert_candidates[@]}")"
        fi
        if [[ ${#key_candidates[@]} -eq 0 ]]; then
            error "No *.key file found."
        else
            error "Key candidates: $(join_paths "${key_candidates[@]}")"
        fi
        exit 1
    fi

    SOURCE_CERT="${cert_candidates[0]}"
    SOURCE_KEY="${key_candidates[0]}"
}

validate_sources() {
    local cert_hash key_hash subject san

    if ! openssl x509 -in "$SOURCE_CERT" -noout >/dev/null 2>&1; then
        error "Invalid certificate: $SOURCE_CERT"
        exit 1
    fi

    if ! openssl pkey -in "$SOURCE_KEY" -noout >/dev/null 2>&1; then
        error "Invalid private key: $SOURCE_KEY"
        exit 1
    fi

    cert_hash=$(openssl x509 -in "$SOURCE_CERT" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 | awk '{print $2}')
    key_hash=$(openssl pkey -in "$SOURCE_KEY" -pubout | openssl pkey -pubin -outform der | openssl dgst -sha256 | awk '{print $2}')

    if [[ "$cert_hash" != "$key_hash" ]]; then
        error "Certificate and key do not match."
        exit 1
    fi

    subject=$(openssl x509 -in "$SOURCE_CERT" -noout -subject 2>/dev/null || true)
    san=$(openssl x509 -in "$SOURCE_CERT" -noout -ext subjectAltName 2>/dev/null || true)

    if [[ "$subject" == *"$DOMAIN"* || "$san" == *"DNS:$DOMAIN"* || "$san" == *"DNS:*.$DOMAIN"* ]]; then
        return 0
    fi

    warn "Certificate subject/SAN does not clearly mention $DOMAIN."
    warn "Subject: ${subject#subject=}"
    warn "SAN: ${san:-<none>}"
    if ! confirm "Continue anyway" "N"; then
        error "Aborted by user"
        exit 1
    fi
}

prepare_targets() {
    TARGET_DIR="/etc/letsencrypt/live/$DOMAIN"
    TARGET_CERT="$TARGET_DIR/fullchain.pem"
    TARGET_KEY="$TARGET_DIR/privkey.pem"
}

print_summary() {
    local backup_cert="(new file)"
    local backup_key="(new file)"
    local services="nginx"

    if [[ -f "$TARGET_CERT" ]]; then
        backup_cert="$TARGET_CERT.bak-$BACKUP_SUFFIX"
    fi
    if [[ -f "$TARGET_KEY" ]]; then
        backup_key="$TARGET_KEY.bak-$BACKUP_SUFFIX"
    fi

    if $DEPLOY_PROXY_NEEDED; then
        services="nginx, wg-mgmt via deploy-proxy"
    fi

    header "Deployment Summary"
    info "Domain:        $DOMAIN"
    info "Source cert:   $(normalize_path "$SOURCE_CERT")"
    info "Source key:    $(normalize_path "$SOURCE_KEY")"
    info "Target cert:   $TARGET_CERT"
    info "Target key:    $TARGET_KEY"
    info "Backups:       $backup_cert"
    info "               $backup_key"
    info "Services:      $services"
    echo ""

    if ! confirm "Proceed with installation" "Y"; then
        error "Aborted by user"
        exit 1
    fi
}

install_targets() {
    install -d -m 755 -o root -g root "$TARGET_DIR"

    if [[ -f "$TARGET_CERT" ]]; then
        CERT_BACKUP="$TARGET_CERT.bak-$BACKUP_SUFFIX"
        cp -a "$TARGET_CERT" "$CERT_BACKUP"
        CERT_BACKUP_CREATED=true
    fi
    if [[ -f "$TARGET_KEY" ]]; then
        KEY_BACKUP="$TARGET_KEY.bak-$BACKUP_SUFFIX"
        cp -a "$TARGET_KEY" "$KEY_BACKUP"
        KEY_BACKUP_CREATED=true
    fi

    if ! install -m 644 -o root -g root "$SOURCE_CERT" "$TARGET_CERT"; then
        rollback_targets
        error "Failed to install certificate"
        return 1
    fi
    CERT_INSTALLED=true
    if ! install -m 600 -o root -g root "$SOURCE_KEY" "$TARGET_KEY"; then
        rollback_targets
        error "Failed to install private key"
        return 1
    fi
    KEY_INSTALLED=true
}

rollback_targets() {
    if $CERT_INSTALLED; then
        if $CERT_BACKUP_CREATED; then
            cp -a "$CERT_BACKUP" "$TARGET_CERT"
        else
            rm -f "$TARGET_CERT"
        fi
    fi

    if $KEY_INSTALLED; then
        if $KEY_BACKUP_CREATED; then
            cp -a "$KEY_BACKUP" "$TARGET_KEY"
        else
            rm -f "$TARGET_KEY"
        fi
    fi
}

validate_nginx() {
    if ! command -v nginx &>/dev/null; then
        rollback_targets
        error "nginx is not installed."
        exit 1
    fi

    if nginx -t; then
        log "nginx configuration is valid"
        return 0
    fi

    warn "nginx validation failed; rolling back certificate files"
    rollback_targets

    if nginx -t; then
        error "nginx validation failed before rollback, but passed after rollback"
    else
        error "nginx validation still fails after rollback"
    fi
    exit 1
}

nginx_references_target() {
    grep -Rqs --fixed-strings "$TARGET_CERT" /etc/nginx 2>/dev/null && \
        grep -Rqs --fixed-strings "$DOMAIN" /etc/nginx 2>/dev/null
}

maybe_run_deploy_proxy() {
    if ! $DEPLOY_PROXY_NEEDED; then
        return 0
    fi

    warn "nginx config does not yet reference this domain/cert."
    if ! confirm "Run server/deploy-proxy.sh --nginx now" "N"; then
        return 0
    fi

    DEPLOY_PROXY_RAN=true
    if bash "$SCRIPT_DIR/deploy-proxy.sh" --nginx; then
        log "deploy-proxy completed"
    else
        error "deploy-proxy failed; rolling back certificate files"
        rollback_targets
        exit 1
    fi
}

reload_nginx() {
    if ! confirm "Reload nginx now" "Y"; then
        warn "Skipped nginx reload"
        return 0
    fi

    if systemctl is-active --quiet nginx 2>/dev/null; then
        if ! systemctl reload nginx; then
            rollback_targets
            error "Failed to reload nginx"
            exit 1
        fi
    else
        if ! systemctl enable --now nginx; then
            rollback_targets
            error "Failed to enable/start nginx"
            exit 1
        fi
    fi

    log "nginx reloaded"
}

detect_proxy_update_need() {
    if nginx_references_target; then
        DEPLOY_PROXY_NEEDED=false
    else
        DEPLOY_PROXY_NEEDED=true
    fi
}

main() {
    if [[ $# -lt 1 || $# -gt 2 ]]; then
        usage
        exit 1
    fi

    CERT_DIR="$1"
    DOMAIN="${2:-}"

    if [[ ! -d "$CERT_DIR" ]]; then
        error "Certificate directory not found: $CERT_DIR"
        exit 1
    fi

    load_server_host

    if [[ -z "$DOMAIN" ]]; then
        prompt_domain
    fi

    prepare_targets
    resolve_certificate_sources
    validate_sources
    detect_proxy_update_need
    print_summary
    install_targets
    validate_nginx
    maybe_run_deploy_proxy

    if ! $DEPLOY_PROXY_RAN; then
        reload_nginx
    fi

    header "Done"
    log "Certificate installed for $DOMAIN"
    log "Target: $TARGET_CERT / $TARGET_KEY"
}

main "$@"
