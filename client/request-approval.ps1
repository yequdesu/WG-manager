# WireGuard Management Layer - Request Approval Client (Windows PowerShell)
# Usage: powershell -ExecutionPolicy Bypass .\request-approval.ps1 -ServerIp <IP> -MgmtPort <PORT>
param(
    [string]$ServerIp,
    [int]$MgmtPort = 58880,
    [string]$PeerName = $env:COMPUTERNAME,
    [string]$Dns = "1.1.1.1,8.8.8.8",
    [int]$PollInterval = 10,
    [int]$PollTimeout = 300
)

if (-not $ServerIp) {
    Write-Host "Usage: .\request-approval.ps1 -ServerIp <IP> [-MgmtPort 58880] [-PeerName MYPC] [-Dns 1.1.1.1]" -ForegroundColor Red
    Write-Host "Example: .\request-approval.ps1 -ServerIp 1.2.3.4" -ForegroundColor Yellow
    exit 1
}

$ErrorActionPreference = "Stop"
$BaseUrl = "http://${ServerIp}:${MgmtPort}"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  WG-Manager - Request Access (Windows)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# ── Phase 1: Submit request ──────────────────────────
Write-Host "[+] Submitting access request as '$PeerName' to ${ServerIp}:${MgmtPort} ..." -ForegroundColor Green
$body = '{"hostname":"' + $PeerName + '","dns":"' + $Dns + '"}'

try {
    $resp = Invoke-RestMethod -Uri "$BaseUrl/api/v1/request" `
        -Method Post -Body $body -ContentType "application/json" `
        -TimeoutSec 10
} catch {
    Write-Host "[x] Failed to submit: $_" -ForegroundColor Red
    exit 1
}

$requestId = $resp.request_id
Write-Host "[+] Request ID: $requestId" -ForegroundColor Green
Write-Host "[!] Waiting for admin approval..." -ForegroundColor Yellow
Write-Host "    Admin must run: wg-mgmt-tui (or curl approve command)"
Write-Host ""

# ── Phase 2: Poll for approval ───────────────────────
$elapsed = 0
$approved = $false
$peerConfig = $null

while ($elapsed -lt $PollTimeout) {
    Start-Sleep -Seconds $PollInterval
    $elapsed += $PollInterval

    try {
        $statusResp = Invoke-RestMethod -Uri "$BaseUrl/api/v1/request/$requestId" -TimeoutSec 5
    } catch {
        Write-Host "[${elapsed}s] Connection issue, retrying..." -ForegroundColor DarkYellow
        continue
    }

    switch ($statusResp.status) {
        "pending" {
            Write-Host "[${elapsed}s] Still waiting..." -ForegroundColor DarkGray
        }
        "expired" {
            Write-Host "[x] Request has expired. Please submit a new one." -ForegroundColor Red
            exit 1
        }
        "not_found" {
            Write-Host "[!] Request not found. Checking if peer was approved..." -ForegroundColor Yellow
            # Fallback: try fetching config directly
            try {
                $conf = Invoke-RestMethod -Uri "$BaseUrl/api/v1/windows-config?name=$PeerName" `
                    -TimeoutSec 5 -ErrorAction Stop
                if ($conf -match "PrivateKey") {
                    $peerConfig = $conf
                    $approved = $true
                    Write-Host "[+] Config found! Request was approved." -ForegroundColor Green
                    break
                }
            } catch { }
            exit 1
        }
        default {
            Write-Host "[${elapsed}s] Unknown status: $($statusResp.status)" -ForegroundColor Yellow
        }
    }

    if ($approved) { break }
}

# Timeout
if (-not $approved) {
    Write-Host ""
    Write-Host "[!] Polling timed out (${PollTimeout}s)." -ForegroundColor Yellow
    Write-Host "    Your request ID: $requestId may still be pending."
    Write-Host "    Contact your admin or run this script again."
    exit 1
}

# ── Phase 3: Download config ──────────────────────────
if (-not $peerConfig) {
    Write-Host "[+] Downloading config..." -ForegroundColor Green
    $peerConfig = Invoke-RestMethod -Uri "$BaseUrl/api/v1/windows-config?name=$PeerName" `
        -TimeoutSec 10 -ErrorAction Stop
}

$confPath = "$env:TEMP\wg0.conf"
$peerConfig | Out-File -Encoding ascii $confPath
Write-Host "[+] Config saved: $confPath" -ForegroundColor Green

# ── Phase 4: Install WireGuard ─────────────────────────
$wgPath = "$env:ProgramFiles\WireGuard\wireguard.exe"
if (Test-Path $wgPath) {
    Write-Host "[+] Importing tunnel..." -ForegroundColor Green
    & $wgPath /installtunnelservice $confPath 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[+] Tunnel installed and started!" -ForegroundColor Green
    } else {
        Write-Host "[!] Tunnel install may have failed. Import manually:" -ForegroundColor Yellow
        Write-Host "    Open WireGuard -> Import Tunnel(s) -> $confPath" -ForegroundColor Yellow
    }
} else {
    Write-Host "[!] WireGuard not installed." -ForegroundColor Yellow
    Write-Host "    Download: https://download.wireguard.com/windows-client/" -ForegroundColor Yellow
    Write-Host "    Then import: $confPath" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "[+] Configuration:" -ForegroundColor Cyan
Get-Content $confPath
Write-Host "[+] Done." -ForegroundColor Green
