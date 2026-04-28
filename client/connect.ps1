# WireGuard Management Layer - Windows Client Script
# Usage in CMD or PowerShell:
#   powershell -NoProfile -ExecutionPolicy Bypass -File connect.ps1

param(
    [string]$ServerIp,
    [int]$MgmtPort,
    [string]$ApiKey,
    [string]$PeerName = $env:COMPUTERNAME,
    [string]$Dns = ""
)

if (-not $ServerIp -or -not $MgmtPort -or -not $ApiKey) {
    Write-Host "Usage: powershell -ExecutionPolicy Bypass .\connect.ps1 -ServerIp <IP> -MgmtPort <PORT> -ApiKey <KEY>" -ForegroundColor Red
    Write-Host ""
    Write-Host "Or let the server generate this script for you:"
    Write-Host "  curl -sSf http://<IP>:<PORT>/api/v1/client-script | sudo bash    (Linux/macOS)"
    Write-Host "  Invoke-WebRequest http://<IP>:<PORT>/api/v1/windows-config?name=$env:COMPUTERNAME -OutFile wg0.conf    (Windows)"
    exit 1
}

$ErrorActionPreference = "Stop"

Write-Host "--- WireGuard Client Setup for Windows ---" -ForegroundColor Cyan

# ── Step 1: Check WireGuard ────────────────────
$wgPath = "$env:ProgramFiles\WireGuard\wireguard.exe"
if (-not (Test-Path $wgPath)) {
    Write-Host "[!] WireGuard not found" -ForegroundColor Yellow
    Write-Host "    Download and install from: https://download.wireguard.com/windows-client/" -ForegroundColor Yellow
    Write-Host "    Then re-run this script." -ForegroundColor Yellow
    exit 1
}
Write-Host "[+] WireGuard found: $wgPath" -ForegroundColor Green

# ── Step 2: Download config ────────────────────
$uri = "http://${ServerIp}:${MgmtPort}/api/v1/windows-config?name=${PeerName}"
if ($Dns) { $uri += "&dns=$Dns" }

$confPath = "$env:TEMP\wg0.conf"
Write-Host "[+] Downloading config from $uri ..." -ForegroundColor Green

try {
    Invoke-WebRequest -Uri $uri `
        -Headers @{Authorization="Bearer $ApiKey"} `
        -OutFile $confPath `
        -ErrorAction Stop
} catch {
    if ($_.Exception.Response.StatusCode -eq 409) {
        Write-Host "[!] Peer '$PeerName' already exists. Fetching existing config..." -ForegroundColor Yellow
        Invoke-WebRequest -Uri $uri `
            -Headers @{Authorization="Bearer $ApiKey"} `
            -OutFile $confPath `
            -ErrorAction Stop
    } else {
        Write-Host "[x] Failed to download config: $_" -ForegroundColor Red
        exit 1
    }
}
Write-Host "[+] Config saved to $confPath" -ForegroundColor Green

# ── Step 3: Import into WireGuard ───────────────
Write-Host "[+] Importing tunnel into WireGuard..." -ForegroundColor Green
$result = & $wgPath /installtunnelservice $confPath 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-Host "[+] Tunnel installed and started" -ForegroundColor Green
} else {
    Write-Host "[!] Tunnel install output: $result" -ForegroundColor Yellow
    Write-Host "[!] You can manually import the file: $confPath" -ForegroundColor Yellow
    Write-Host "    Open WireGuard -> Import Tunnel(s) -> select wg0.conf" -ForegroundColor Yellow
}

# ── Step 4: Verify config content ───────────────
Write-Host ""
Write-Host "--- Configuration ---" -ForegroundColor Cyan
Get-Content $confPath
Write-Host "--- Done ---" -ForegroundColor Cyan
Write-Host ""
Write-Host "Your VPN IP should now appear in the WireGuard system tray icon." -ForegroundColor Green
Write-Host "To verify: wg show wg0"
