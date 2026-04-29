#!/bin/bash
# Platform abstraction library — sourced by all Linux client scripts

# ── OS detection ─────────────────────────────────
detect_os() {
    if [[ "$(uname)" == "Darwin" ]]; then OS="macos"; INIT="none"; PKG="brew"; return 0; fi
    if [[ ! -f /etc/os-release ]]; then OS="unknown"; return 1; fi
    . /etc/os-release
    OS="$ID"
    case "$OS" in
        ubuntu|debian)  PKG="apt";  INIT="systemd" ;;
        fedora|centos|rhel|rocky|alma) PKG="dnf"; INIT="systemd" ;;
        arch)           PKG="pacman"; INIT="systemd" ;;
        alpine)         PKG="apk";   INIT="openrc" ;;
        *)              PKG="unknown"; INIT="none" ;;
    esac
}

# ── WireGuard installation (interactive) ─────────
ensure_wireguard() {
    if command -v wg &>/dev/null; then
        echo "[+] WireGuard tools already installed ($(wg --version 2>&1 | head -1))"
        return 0
    fi
    echo "[!] WireGuard not installed."
    if [[ -t 0 ]]; then
        read -r -p "    Install now? [Y/n]: " choice
        if [[ "$choice" =~ ^[Nn] ]]; then
            echo "    Install wireguard-tools manually and re-run."
            exit 1
        fi
    fi
    case "$PKG" in
        apk)    apk add wireguard-tools ;;
        apt)    apt-get update -qq; apt-get install -y wireguard wireguard-tools ;;
        dnf)    dnf install -y wireguard-tools ;;
        yum)    yum install -y epel-release; yum install -y wireguard-tools ;;
        pacman) pacman -Sy --noconfirm wireguard-tools ;;
        brew)   brew install wireguard-tools ;;
        *)      echo "Unknown package manager. Install wireguard-tools manually."; exit 1 ;;
    esac
    echo "[+] WireGuard installed"
}

# ── WireGuard service management ──────────────────
wg_service() {
    local iface="${1:-wg0}"
    case "$INIT" in
        systemd)
            systemctl enable "wg-quick@$iface" --quiet 2>/dev/null || true
            systemctl restart "wg-quick@$iface"
            ;;
        openrc)
            rc-update add "wg-quick@$iface" 2>/dev/null || true
            rc-service "wg-quick@$iface" restart
            ;;
        *)
            wg-quick up "$iface" &
            ;;
    esac
}

# Auto-sudo: re-exec with sudo if not root
auto_sudo() {
    if [[ "$(id -u)" -ne 0 ]]; then
        if [[ -f "$0" ]]; then
            exec sudo bash "$0" "$@"
        else
            echo "[x] This script must be run as root. Use: curl ... | sudo bash"
            exit 1
        fi
    fi
}
