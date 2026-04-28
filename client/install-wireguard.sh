#!/bin/bash
# WireGuard Management Layer - Multi-OS WG Install Helper
# Source this file or use as standalone

install_wireguard() {
    local OS="$1"

    if command -v wg &>/dev/null; then
        return 0
    fi

    case "$OS" in
        ubuntu|debian)
            apt-get update -qq && apt-get install -y wireguard wireguard-tools
            ;;
        fedora|centos|rhel|rocky|alma)
            if command -v dnf &>/dev/null; then
                dnf install -y wireguard-tools
            else
                yum install -y epel-release && yum install -y wireguard-tools
            fi
            ;;
        arch)
            pacman -Sy --noconfirm wireguard-tools
            ;;
        macos)
            brew install wireguard-tools
            ;;
        *)
            echo "Unsupported OS: $OS"
            return 1
            ;;
    esac
}

detect_os() {
    if [[ "$(uname)" == "Darwin" ]]; then
        echo "macos"
    elif [[ -f /etc/os-release ]]; then
        . /etc/os-release
        echo "$ID"
    elif command -v apt-get &>/dev/null; then
        echo "debian"
    elif command -v dnf &>/dev/null; then
        echo "fedora"
    elif command -v yum &>/dev/null; then
        echo "centos"
    elif command -v pacman &>/dev/null; then
        echo "arch"
    else
        echo "unknown"
    fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    OS=$(detect_os)
    echo "Detected OS: $OS"
    install_wireguard "$OS" && echo "WireGuard installed" || echo "Failed to install"
fi
