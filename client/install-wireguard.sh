#!/bin/bash
# WireGuard Management Layer — Multi-OS WG Install Helper
# Sources os-detect.sh for platform abstraction.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/os-detect.sh"

detect_os
ensure_wireguard
