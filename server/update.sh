#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$PROJECT_DIR/config.env"
PEERS_FILE="$PROJECT_DIR/server/peers.json"
BIN_DIR="$PROJECT_DIR/bin"
DAEMON_BIN="$BIN_DIR/wg-mgmt-daemon"
CLI_BIN="$BIN_DIR/wg-mgmt"
DAEMON_SERVICE="wg-mgmt"
BACKUP_TS="$(date +%Y%m%d-%H%M%S)"
CONFIG_BACKUP=""
PEERS_BACKUP=""
REMOTE_NAME="origin"
REMOTE_REF=""
PRE_UPDATE_HEAD=""
BEHIND_COUNT=0
AHEAD_COUNT=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info() { printf '%b[INFO]%b %s\n' "$CYAN" "$NC" "$*"; }
warn() { printf '%b[WARN]%b %s\n' "$YELLOW" "$NC" "$*"; }
error() { printf '%b[ERROR]%b %s\n' "$RED" "$NC" "$*"; }
success() { printf '%b[OK]%b %s\n' "$GREEN" "$NC" "$*"; }

die() {
    error "$*"
    exit 1
}

require_command() {
    local command_name="$1"
    command -v "$command_name" >/dev/null 2>&1 || die "Missing required command: $command_name"
}

run_root() {
    if [[ "$(id -u)" -eq 0 ]]; then
        "$@"
    else
        require_command sudo
        sudo "$@"
    fi
}

get_config_value() {
    local key="$1"
    grep -E "^${key}=" "$CONFIG_FILE" 2>/dev/null | head -n1 | cut -d= -f2-
}

resolve_upstream_ref() {
    local upstream_ref
    upstream_ref="$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null || true)"

    if [[ -n "$upstream_ref" ]]; then
        printf '%s\n' "$upstream_ref"
        return 0
    fi

    local remote_head
    remote_head="$(git symbolic-ref --quiet --short "refs/remotes/${REMOTE_NAME}/HEAD" 2>/dev/null || true)"
    if [[ -n "$remote_head" ]]; then
        printf '%s\n' "$remote_head"
        return 0
    fi

    for candidate in "${REMOTE_NAME}/main" "${REMOTE_NAME}/master"; do
        if git rev-parse --verify --quiet "$candidate" >/dev/null; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    return 1
}

check_daemon_health() {
    local mgmt_listen mgmt_host mgmt_port health_code
    mgmt_listen="$(get_config_value MGMT_LISTEN)"
    [[ -n "$mgmt_listen" ]] || die "MGMT_LISTEN is not set in config.env"

    mgmt_host="${mgmt_listen%:*}"
    mgmt_port="${mgmt_listen##*:}"
    [[ -n "$mgmt_port" ]] || mgmt_port="58880"
    [[ "$mgmt_host" == "0.0.0.0" ]] && mgmt_host="127.0.0.1"

    for service_name in wg-quick@wg0 "$DAEMON_SERVICE"; do
        systemctl is-active --quiet "$service_name" 2>/dev/null || die "$service_name is not running"
    done

    health_code="$(curl -s -o /dev/null -w '%{http_code}' "http://${mgmt_host}:${mgmt_port}/api/v1/health" 2>/dev/null || printf '000')"
    [[ "$health_code" == "200" ]] || die "Daemon health check failed (HTTP $health_code)"
}

check_preflight() {
    info "Preflight checks"
    require_command git
    require_command make
    require_command go
    require_command curl
    require_command systemctl

    [[ -f "$CONFIG_FILE" ]] || die "Missing config: $CONFIG_FILE"
    [[ -f "$PEERS_FILE" ]] || die "Missing peers database: $PEERS_FILE"

    check_daemon_health
    success "Daemon health looks good"
}

show_pending_changes() {
    info "Fetching remote updates"
    git fetch --prune "$REMOTE_NAME"

    REMOTE_REF="$(resolve_upstream_ref)" || die "Could not determine upstream branch"
    PRE_UPDATE_HEAD="$(git rev-parse HEAD)"

    echo ""
    git status --short --branch
    echo ""

    read -r BEHIND_COUNT AHEAD_COUNT < <(git rev-list --left-right --count "HEAD...$REMOTE_REF")

    info "Current branch: $(git branch --show-current)"
    info "Upstream ref:   $REMOTE_REF"
    info "Ahead/behind:   ${AHEAD_COUNT}/${BEHIND_COUNT}"

    if [[ "$BEHIND_COUNT" -gt 0 ]]; then
        echo ""
        info "Incoming commits"
        git log --oneline --decorate --graph --max-count=20 "HEAD..$REMOTE_REF"
    else
        info "No incoming commits found"
    fi

    if [[ "$AHEAD_COUNT" -gt 0 ]]; then
        warn "Local commits exist ahead of upstream; pull may need manual resolution"
    fi
}

confirm_update() {
    local answer
    read -r -p "Update to latest? [y/N] " answer
    [[ "$answer" =~ ^[Yy]$ ]]
}

backup_file() {
    local source_file="$1"
    local backup_file_path="${source_file}.bak-${BACKUP_TS}"

    if ! cp -a "$source_file" "$backup_file_path" 2>/dev/null; then
        run_root cp -a "$source_file" "$backup_file_path"
    fi

    printf '%s\n' "$backup_file_path"
}

perform_update() {
    local current_branch
    current_branch="$(git branch --show-current)"
    [[ -n "$current_branch" ]] || die "Detached HEAD is not supported for updates"

    info "Creating backups"
    CONFIG_BACKUP="$(backup_file "$CONFIG_FILE")"
    PEERS_BACKUP="$(backup_file "$PEERS_FILE")"
    success "Backups saved"
    info "  $CONFIG_BACKUP"
    info "  $PEERS_BACKUP"

    info "Pulling latest code"
    git pull --ff-only "$REMOTE_NAME" "$current_branch"

    info "Building daemon and CLI"
    (cd "$PROJECT_DIR" && GOOS=linux GOARCH="$(go env GOARCH)" make build && GOOS=linux GOARCH="$(go env GOARCH)" make build-cli)

    [[ -f "$DAEMON_BIN" ]] || die "Daemon build artifact missing: $DAEMON_BIN"
    [[ -f "$CLI_BIN" ]] || die "CLI build artifact missing: $CLI_BIN"

    info "Installing updated binaries"
    run_root install -m 755 "$DAEMON_BIN" /usr/local/bin/wg-mgmt-daemon
    run_root install -m 755 "$CLI_BIN" /usr/local/bin/wg-mgmt

    info "Restarting daemon"
    run_root systemctl restart "$DAEMON_SERVICE"
    sleep 2

    info "Post-update health check"
    check_daemon_health
}

offer_tui_update() {
    [[ -f "$PROJECT_DIR/wg-tui/install.sh" ]] || return 0

    local answer
    read -r -p "Rebuild Rust TUI too? [y/N] " answer
    if [[ ! "$answer" =~ ^[Yy]$ ]]; then
        return 0
    fi

    info "Rebuilding Rust TUI"
    if (cd "$PROJECT_DIR/wg-tui" && bash install.sh); then
        success "Rust TUI updated"
    else
        warn "Rust TUI rebuild failed; main daemon update remains complete"
    fi
}

print_rollback_guidance() {
    echo ""
    warn "Rollback guidance"
    echo "  1. Restore backups:"
    [[ -n "$CONFIG_BACKUP" ]] && echo "     sudo cp '$CONFIG_BACKUP' '$CONFIG_FILE'"
    [[ -n "$PEERS_BACKUP" ]] && echo "     sudo cp '$PEERS_BACKUP' '$PEERS_FILE'"
    echo "  2. Reinstall the previous code state:"
    echo "     git checkout '$PRE_UPDATE_HEAD'"
    echo "     make build && make build-cli"
    echo "     sudo install -m 755 bin/wg-mgmt-daemon /usr/local/bin/wg-mgmt-daemon"
    echo "     sudo install -m 755 bin/wg-mgmt /usr/local/bin/wg-mgmt"
    echo "  3. Restart and re-check:"
    echo "     sudo systemctl restart $DAEMON_SERVICE"
    echo "     bash scripts/health-check.sh"
}

main() {
    check_preflight
    show_pending_changes

    if [[ "$BEHIND_COUNT" -eq 0 ]]; then
        info "Already up to date; nothing to install"
        exit 0
    fi

    if ! confirm_update; then
        info "Update cancelled"
        exit 0
    fi

    if ! perform_update; then
        error "Update failed"
        print_rollback_guidance
        exit 1
    fi

    offer_tui_update
    success "Update complete"
}

main "$@"
