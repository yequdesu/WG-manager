# WireGuard Management Layer - Request Approval Client (Windows PowerShell)
# Usage: powershell -ExecutionPolicy Bypass .\request-approval.ps1 -ServerIp <IP> -PeerName <NAME>
param(
    [string]$ServerIp = "__SERVER_IP__",
    [int]$MgmtPort = __MGMT_PORT__,
    [string]$PeerName = "",
    [string]$Dns = "1.1.1.1,8.8.8.8",
    [int]$PollInterval = 10,
    [int]$PollTimeout = 300
)

if (-not $ServerIp) {
    Write-Host "Usage: .\request-approval.ps1 -ServerIp <IP> -PeerName <NAME> [-MgmtPort 58880] [-Dns 1.1.1.1]" -ForegroundColor Red
    exit 1
}
if (-not $PeerName) {
    Write-Host "Usage: .\request-approval.ps1 -ServerIp <IP> -PeerName <NAME>" -ForegroundColor Red
    Write-Host "  Peer name is required to identify your device." -ForegroundColor Yellow
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
        -Method Post -Body $body -ContentType "application/json" -TimeoutSec 10
} catch {
    Write-Host "[x] Failed to submit: $_" -ForegroundColor Red
    exit 1
}

$requestId = $resp.request_id
Write-Host "[+] Request ID: $requestId" -ForegroundColor Green
Write-Host "[!] Waiting for admin approval..." -ForegroundColor Yellow
Write-Host "    (Admin can use wg-mgmt-tui or curl to approve)"
Write-Host ""

# ── Phase 2: Poll for approval ───────────────────────
$elapsed = 0
$approved = $false
$peerConf = $null

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
        "approved" {
            $approved = $true
            $peerConf = "[Interface]`n" +
                "Address = $($statusResp.peer.address)`n" +
                "PrivateKey = $($statusResp.peer.private_key)`n" +
                "DNS = $($statusResp.peer.dns)`n" +
                "`n[Peer]`n" +
                "PublicKey = $($statusResp.peer.server_public_key)`n" +
                "Endpoint = $($statusResp.peer.server_endpoint)`n" +
                "AllowedIPs = 10.0.0.0/24`n" +
                "PersistentKeepalive = $($statusResp.peer.keepalive)"
            Write-Host "[+] Request APPROVED!" -ForegroundColor Green
            break
        }
        "rejected" {
            Write-Host "[x] Request was REJECTED by admin." -ForegroundColor Red
            exit 1
        }
        "expired" {
            Write-Host "[x] Request has EXPIRED. Please submit a new one." -ForegroundColor Red
            exit 1
        }
        "not_found" {
            Write-Host "[x] Request not found. It may have been processed already." -ForegroundColor Red
            exit 1
        }
        default {
            Write-Host "[${elapsed}s] Status: $($statusResp.status)" -ForegroundColor Yellow
            Write-Host "    Raw response: $statusResp" -ForegroundColor DarkGray
        }
    }

    if ($approved) { break }
}

if (-not $approved) {
    Write-Host ""
    Write-Host "[!] Polling timed out (${PollTimeout}s)." -ForegroundColor Yellow
    Write-Host "    Request ID: $requestId may still be pending." -ForegroundColor Yellow
    Write-Host "    Contact your admin or run this script again." -ForegroundColor Yellow
    exit 1
}

# ── Phase 3: Save config + manual import instructions ──
$confPath = "$env:TEMP\wg0.conf"
$peerConf | Out-File -Encoding ascii $confPath
Write-Host "[+] Config saved: $confPath" -ForegroundColor Green
Write-Host ""
Write-Host "To connect:" -ForegroundColor Cyan
Write-Host "  1. Download WireGuard: https://download.wireguard.com/windows-client/" -ForegroundColor White
Write-Host "  2. Install and open WireGuard" -ForegroundColor White
Write-Host "  3. Click 'Import Tunnel(s) from file'" -ForegroundColor White
Write-Host "  4. Select: $confPath" -ForegroundColor White
Write-Host "  5. Click 'Activate'" -ForegroundColor White
Write-Host ""
Write-Host "Configuration preview:" -ForegroundColor Cyan
Write-Host $peerConf
Write-Host ""
Write-Host "[+] Done." -ForegroundColor Green
