#!/bin/bash
set -euo pipefail

# Cross-compile Go binary for Linux amd64 from any platform
# Usage: ./scripts/build.sh [output_dir]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${1:-$PROJECT_DIR/bin}"
BINARY_NAME="wg-mgmt-daemon"

echo "Building WireGuard Management Daemon..."
echo "Target: linux/amd64"
echo "Output: $OUTPUT_DIR/$BINARY_NAME"

cd "$PROJECT_DIR"

mkdir -p "$OUTPUT_DIR"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags="-s -w" \
    -o "$OUTPUT_DIR/$BINARY_NAME" \
    ./cmd/mgmt-daemon/

echo "Build complete: $OUTPUT_DIR/$BINARY_NAME"
file "$OUTPUT_DIR/$BINARY_NAME" 2>/dev/null || true
ls -lh "$OUTPUT_DIR/$BINARY_NAME" 2>/dev/null || true
