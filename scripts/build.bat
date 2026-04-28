@echo off
REM Cross-compile Go binary for Linux amd64 from Windows
REM Usage: build.bat

set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0

echo Building WireGuard Management Daemon...
echo Target: linux/amd64
echo Output: bin/wg-mgmt-daemon

if not exist bin mkdir bin

go build -ldflags="-s -w" -o bin\wg-mgmt-daemon .\cmd\mgmt-daemon\

echo Build complete: bin\wg-mgmt-daemon
dir bin\wg-mgmt-daemon 2>nul
