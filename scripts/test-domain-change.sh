#!/bin/bash

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

FAILURES=0
FIXED_TIMESTAMP=""

date() {
    if [[ "${1:-}" == '+%Y%m%d-%H%M%S' && -n "$FIXED_TIMESTAMP" ]]; then
        printf '%s\n' "$FIXED_TIMESTAMP"
        return 0
    fi

    command date "$@"
}

log() { :; }
warn() { :; }
info() { :; }
header() { :; }
error() { :; }

is_ip_address() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]
}

load_functions_from_script() {
    local script_path="$1"

    eval "$(sed -n '/^update_config_value()/,/^}$/p' "$script_path")"
    eval "$(sed -n '/^sync_config_env()/,/^}$/p' "$script_path")"
}

cleanup_dir() {
    local dir_path="$1"

    [[ -n "$dir_path" && -d "$dir_path" ]] && rm -rf "$dir_path"
}

fail_test() {
    local test_name="$1"
    local reason="$2"

    printf 'FAIL: %s: %s\n' "$test_name" "$reason"
    FAILURES=$((FAILURES + 1))
}

pass_test() {
    printf 'PASS: %s\n' "$1"
}

assert_equals() {
    local expected="$1"
    local actual="$2"
    local reason="$3"

    if [[ "$actual" != "$expected" ]]; then
        printf '%s (expected %q, got %q)' "$reason" "$expected" "$actual"
        return 1
    fi

    return 0
}

assert_file_exists() {
    local file_path="$1"
    local reason="$2"

    if [[ ! -f "$file_path" ]]; then
        printf '%s (%s missing)' "$reason" "$file_path"
        return 1
    fi

    return 0
}

assert_file_line() {
    local file_path="$1"
    local pattern="$2"
    local expected="$3"
    local reason="$4"
    local actual

    actual="$(grep -m1 "$pattern" "$file_path" 2>/dev/null || true)"
    if ! assert_equals "$expected" "$actual" "$reason"; then
        return 1
    fi

    return 0
}

assert_single_backup() {
    local dir_path="$1"
    local expected_count="$2"
    local backups=()
    local count

    shopt -s nullglob
    backups=("$dir_path"/config.env.bak-*)
    shopt -u nullglob

    count=${#backups[@]}
    if [[ "$count" -ne "$expected_count" ]]; then
        printf 'expected %s backup file(s), found %s' "$expected_count" "$count"
        return 1
    fi

    return 0
}

assert_md5_unchanged() {
    local first_hash="$1"
    local second_hash="$2"
    local reason="$3"

    if [[ "$first_hash" != "$second_hash" ]]; then
        printf '%s (expected %s, got %s)' "$reason" "$first_hash" "$second_hash"
        return 1
    fi

    return 0
}

run_proxy_domain_change_test() {
    local test_name="deploy-proxy domain change"
    local tmpdir config_file backup_file actual

    tmpdir="$(mktemp -d)" || { fail_test "$test_name" "could not create temp dir"; return 1; }
    config_file="$tmpdir/config.env"
    backup_file="$config_file.bak-20240509-101112"

    printf 'WG_PORT=51820\nSERVER_HOST=old.example.com\n' > "$config_file"

    CONFIG_FILE="$config_file"
    SERVER_NAME="vpn2.example.com"
    FIXED_TIMESTAMP="20240509-101112"
    load_functions_from_script "$ROOT_DIR/server/deploy-proxy.sh"

    sync_config_env

    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=vpn2.example.com' "$actual" "SERVER_HOST not updated"; then
        fail_test "$test_name" "SERVER_HOST not updated"
        cleanup_dir "$tmpdir"
        return 1
    fi

    if ! assert_file_exists "$backup_file" "backup file not created"; then
        fail_test "$test_name" "backup file not created"
        cleanup_dir "$tmpdir"
        return 1
    fi

    pass_test "$test_name"
    cleanup_dir "$tmpdir"
    return 0
}

run_proxy_ip_only_test() {
    local test_name="deploy-proxy IP-only fallback"
    local tmpdir config_file backup_file actual

    tmpdir="$(mktemp -d)" || { fail_test "$test_name" "could not create temp dir"; return 1; }
    config_file="$tmpdir/config.env"
    backup_file="$config_file.bak-20240509-101113"

    printf 'SERVER_HOST=old.example.com\n' > "$config_file"

    CONFIG_FILE="$config_file"
    SERVER_NAME="118.178.171.166"
    FIXED_TIMESTAMP="20240509-101113"
    load_functions_from_script "$ROOT_DIR/server/deploy-proxy.sh"

    sync_config_env

    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=' "$actual" "SERVER_HOST should be cleared"; then
        fail_test "$test_name" "SERVER_HOST should be cleared"
        cleanup_dir "$tmpdir"
        return 1
    fi

    if ! assert_file_exists "$backup_file" "backup file not created"; then
        fail_test "$test_name" "backup file not created"
        cleanup_dir "$tmpdir"
        return 1
    fi

    actual="$(grep -m1 '^SERVER_HOST=' "$backup_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=old.example.com' "$actual" "backup did not preserve old value"; then
        fail_test "$test_name" "backup did not preserve old value"
        cleanup_dir "$tmpdir"
        return 1
    fi

    pass_test "$test_name"
    cleanup_dir "$tmpdir"
    return 0
}

run_proxy_idempotent_test() {
    local test_name="deploy-proxy idempotent rerun"
    local tmpdir config_file first_hash second_hash

    tmpdir="$(mktemp -d)" || { fail_test "$test_name" "could not create temp dir"; return 1; }
    config_file="$tmpdir/config.env"

    printf 'WG_PORT=51820\nSERVER_HOST=same.com\n' > "$config_file"

    CONFIG_FILE="$config_file"
    SERVER_NAME="same.com"
    FIXED_TIMESTAMP="20240509-101114"
    load_functions_from_script "$ROOT_DIR/server/deploy-proxy.sh"

    sync_config_env
    first_hash="$(md5sum "$config_file" | cut -d' ' -f1)"
    sync_config_env
    second_hash="$(md5sum "$config_file" | cut -d' ' -f1)"

    if ! assert_md5_unchanged "$first_hash" "$second_hash" "config changed on second run"; then
        fail_test "$test_name" "config changed on second run"
        cleanup_dir "$tmpdir"
        return 1
    fi

    if ! assert_single_backup "$tmpdir" 1; then
        fail_test "$test_name" "expected one backup file"
        cleanup_dir "$tmpdir"
        return 1
    fi

    pass_test "$test_name"
    cleanup_dir "$tmpdir"
    return 0
}

run_proxy_first_deploy_test() {
    local test_name="deploy-proxy first deploy"
    local tmpdir config_file actual

    tmpdir="$(mktemp -d)" || { fail_test "$test_name" "could not create temp dir"; return 1; }
    config_file="$tmpdir/config.env"

    printf 'WG_PORT=51820\n' > "$config_file"

    CONFIG_FILE="$config_file"
    SERVER_NAME="new.domain.com"
    FIXED_TIMESTAMP="20240509-101115"
    load_functions_from_script "$ROOT_DIR/server/deploy-proxy.sh"

    sync_config_env

    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=new.domain.com' "$actual" "SERVER_HOST missing or incorrect"; then
        fail_test "$test_name" "SERVER_HOST missing or incorrect"
        cleanup_dir "$tmpdir"
        return 1
    fi

    actual="$(grep -m1 '^WG_PORT=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'WG_PORT=51820' "$actual" "WG_PORT not preserved"; then
        fail_test "$test_name" "WG_PORT not preserved"
        cleanup_dir "$tmpdir"
        return 1
    fi

    pass_test "$test_name"
    cleanup_dir "$tmpdir"
    return 0
}

run_nginx_mirror_test() {
    local test_name="deploy-nginx mirrors proxy behavior"
    local tmpdir config_file backup_file actual first_hash second_hash

    tmpdir="$(mktemp -d)" || { fail_test "$test_name" "could not create temp dir"; return 1; }
    config_file="$tmpdir/config.env"
    backup_file="$config_file.bak-20240509-101116"

    load_functions_from_script "$ROOT_DIR/server/deploy-nginx.sh"

    printf 'WG_PORT=51820\nSERVER_HOST=old.example.com\n' > "$config_file"
    CONFIG_FILE="$config_file"
    SERVER_NAME="vpn2.example.com"
    FIXED_TIMESTAMP="20240509-101116"
    sync_config_env
    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=vpn2.example.com' "$actual" "nginx domain update failed"; then
        fail_test "$test_name" "nginx domain update failed"
        cleanup_dir "$tmpdir"
        return 1
    fi
    if ! assert_file_exists "$backup_file" "nginx domain backup missing"; then
        fail_test "$test_name" "nginx domain backup missing"
        cleanup_dir "$tmpdir"
        return 1
    fi

    printf 'SERVER_HOST=old.example.com\n' > "$config_file"
    SERVER_NAME="118.178.171.166"
    FIXED_TIMESTAMP="20240509-101117"
    sync_config_env
    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=' "$actual" "nginx IP fallback failed"; then
        fail_test "$test_name" "nginx IP fallback failed"
        cleanup_dir "$tmpdir"
        return 1
    fi

    printf 'WG_PORT=51820\nSERVER_HOST=same.com\n' > "$config_file"
    SERVER_NAME="same.com"
    FIXED_TIMESTAMP="20240509-101118"
    sync_config_env
    first_hash="$(md5sum "$config_file" | cut -d' ' -f1)"
    sync_config_env
    second_hash="$(md5sum "$config_file" | cut -d' ' -f1)"
    if ! assert_md5_unchanged "$first_hash" "$second_hash" "nginx idempotent rerun changed config"; then
        fail_test "$test_name" "nginx idempotent rerun changed config"
        cleanup_dir "$tmpdir"
        return 1
    fi

    printf 'WG_PORT=51820\n' > "$config_file"
    SERVER_NAME="new.domain.com"
    FIXED_TIMESTAMP="20240509-101119"
    sync_config_env
    actual="$(grep -m1 '^SERVER_HOST=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'SERVER_HOST=new.domain.com' "$actual" "nginx first deploy failed"; then
        fail_test "$test_name" "nginx first deploy failed"
        cleanup_dir "$tmpdir"
        return 1
    fi
    actual="$(grep -m1 '^WG_PORT=' "$config_file" 2>/dev/null || true)"
    if ! assert_equals 'WG_PORT=51820' "$actual" "nginx first deploy lost WG_PORT"; then
        fail_test "$test_name" "nginx first deploy lost WG_PORT"
        cleanup_dir "$tmpdir"
        return 1
    fi

    pass_test "$test_name"
    cleanup_dir "$tmpdir"
    return 0
}

run_hup_command_test() {
    local test_name="SIGHUP command present"
    local proxy_line nginx_line

    proxy_line="$(grep -m1 -F 'systemctl kill -s HUP wg-mgmt' "$ROOT_DIR/server/deploy-proxy.sh" 2>/dev/null || true)"
    nginx_line="$(grep -m1 -F 'systemctl kill -s HUP wg-mgmt' "$ROOT_DIR/server/deploy-nginx.sh" 2>/dev/null || true)"

    if [[ -z "$proxy_line" ]]; then
        fail_test "$test_name" "missing HUP line in deploy-proxy.sh"
        return 1
    fi

    if [[ -z "$nginx_line" ]]; then
        fail_test "$test_name" "missing HUP line in deploy-nginx.sh"
        return 1
    fi

    pass_test "$test_name"
    return 0
}

main() {
    run_proxy_domain_change_test
    run_proxy_ip_only_test
    run_proxy_idempotent_test
    run_proxy_first_deploy_test
    run_nginx_mirror_test
    run_hup_command_test

    if [[ "$FAILURES" -ne 0 ]]; then
        return 1
    fi

    return 0
}

main
