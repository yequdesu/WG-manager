package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"wire-guard-dev/internal/audit"
	"wire-guard-dev/internal/store"
	"wire-guard-dev/internal/wg"
)

type Config struct {
	WGInterface    string
	WGPort         int
	WGSubnet       string
	WGServerIP     string
	MgmtListen     string
	APIKey         string
	ServerPublicIP string
	DefaultDNS     string
	PeerKeepalive  int
	PeersDBPath    string
	WGConfPath     string
}

func (c *Config) ServerEndpoint() string {
	return fmt.Sprintf("%s:%d", c.ServerPublicIP, c.WGPort)
}

type Handler struct {
	store     *store.State
	wgMgr     *wg.Manager
	config_   atomic.Pointer[Config]
	wgCache   atomic.Pointer[wgCacheEntry]
}

type wgCacheEntry struct {
	status    *wg.InterfaceStatus
	expiresAt time.Time
}

func (h *Handler) cfg() *Config { return h.config_.Load() }

func (h *Handler) ReloadConfig(cfg *Config) { h.config_.Store(cfg) }

func (h *Handler) wgShowCached(iface string) (*wg.InterfaceStatus, error) {
	entry := h.wgCache.Load()
	if entry != nil && time.Now().Before(entry.expiresAt) {
		return entry.status, nil
	}
	status, err := h.wgMgr.Show(iface)
	if err != nil {
		return nil, err
	}
	h.wgCache.Store(&wgCacheEntry{
		status:    status,
		expiresAt: time.Now().Add(5 * time.Second),
	})
	return status, nil
}

func NewHandler(s *store.State, m *wg.Manager, cfg *Config) *Handler {
	h := &Handler{
		store: s,
		wgMgr: m,
	}
	if cfg != nil {
		h.config_.Store(cfg)
	}
	return h
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "use invite redemption via POST /api/v1/redeem")
}

func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	ua := r.Header.Get("User-Agent")
	isBrowser := strings.Contains(ua, "Mozilla") || strings.Contains(ua, "Chrome") || strings.Contains(ua, "Safari") || strings.Contains(ua, "Firefox") || strings.Contains(ua, "Edge")
	if isBrowser && !strings.Contains(ua, "curl") && !strings.Contains(ua, "PowerShell") && !strings.Contains(ua, "Wget") {
		h.serveBootstrapHTML(w, r)
		return
	}
	writeJSON(w, http.StatusGone, map[string]string{
		"error":   "endpoint deprecated",
		"message": "Use POST /api/v1/redeem to join with an invite token from your administrator.",
	})
}

func (h *Handler) serveDirectBash(w http.ResponseWriter, r *http.Request, name string) {
	script := embedConnectSh
	script = strings.ReplaceAll(script, "__SERVER_PUBLIC_IP__", h.cfg().ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.cfg().MgmtListen))
	script = strings.ReplaceAll(script, "__API_KEY__", h.cfg().APIKey)
	script = strings.ReplaceAll(script, "__DEFAULT_DNS__", h.cfg().DefaultDNS)
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.cfg().WGSubnet)
	script = strings.ReplaceAll(script, "__PEER_NAME__", name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) serveApprovalBash(w http.ResponseWriter, r *http.Request, name string) {
	script := embedApprovalSh
	script = strings.ReplaceAll(script, "__SERVER_IP__", h.cfg().ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.cfg().MgmtListen))
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.cfg().WGSubnet)
	script = strings.ReplaceAll(script, "__DEFAULT_DNS__", h.cfg().DefaultDNS)
	script = strings.ReplaceAll(script, "__PEER_NAME__", name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) serveDirectWin(w http.ResponseWriter, r *http.Request, name string) {
	q := r.URL.Query()
	if name == "" {
		name = strings.TrimSpace(q.Get("name"))
	}
	if name == "" {
		name = "client"
	}
	if err := validatePeerName(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dns := strings.TrimSpace(q.Get("dns"))
	if dns == "" {
		dns = h.cfg().DefaultDNS
	}
	if err := validateDNS(dns); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var privateKey, publicKey, ip string
	if existing, ok := h.store.GetPeer(name); ok {
		privateKey = existing.PrivateKey
		publicKey = existing.PublicKey
		ip = existing.Address
	} else {
		var err error
		privateKey, publicKey, err = h.wgMgr.GenKeyPair()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
			return
		}
		peer := store.Peer{
			Name: name, PublicKey: publicKey, PrivateKey: privateKey,
			DNS: dns, Keepalive: h.cfg().PeerKeepalive,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		ip, err = h.store.AllocateIPAndAddPeer(&peer, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no available IP addresses"})
			return
		}
		if err := h.commitPeerToWG(name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		audit.Log("peer_registered", auditFields("name", name, "ip", ip, "source", remoteIP(r)))
	}

	conf := fmt.Sprintf(`[Interface]
Address = %s/24
PrivateKey = %s
DNS = %s

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s
PersistentKeepalive = %d
`, ip, privateKey, dns, h.store.Server().PublicKey, h.cfg().ServerEndpoint(), h.cfg().WGSubnet, h.cfg().PeerKeepalive)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.conf", name))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(conf))
}

func (h *Handler) serveApprovalPS1(w http.ResponseWriter, r *http.Request) {
	script := embedApprovalPs1
	script = strings.ReplaceAll(script, "__SERVER_IP__", h.cfg().ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.cfg().MgmtListen))
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.cfg().WGSubnet)
	script = strings.ReplaceAll(script, "__DEFAULT_DNS__", h.cfg().DefaultDNS)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request) {
	ip := h.cfg().ServerPublicIP
	p := portStr(h.cfg().MgmtListen)
	wgSubnet := h.cfg().WGSubnet
	html := `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>WG-Manager</title>
<style>*{margin:0;padding:0;box-sizing:border-box}
body{font:14px/1.6 system-ui,-apple-system,monospace;background:#0d1117;color:#c9d1d9;max-width:780px;margin:0 auto;padding:24px 16px}
h1{color:#58a6ff;font-size:20px;margin-bottom:4px}
.sub{color:#8b949e;font-size:12px;margin-bottom:20px}
.tabs{display:flex;gap:0;border-bottom:1px solid #30363d;margin-bottom:16px}
.tab{padding:8px 16px;cursor:pointer;border:1px solid transparent;border-radius:6px 6px 0 0;color:#8b949e;font-size:13px;transition:.15s}
.tab:hover{color:#c9d1d9;background:#161b22}
.tab.active{color:#58a6ff;border-color:#30363d;border-bottom-color:#0d1117;background:#161b22;font-weight:600}
.section{display:none}.section.active{display:block}
.box{background:#161b22;border:1px solid #30363d;border-radius:6px;padding:14px;margin-bottom:12px}
.box h3{font-size:13px;color:#8b949e;margin-bottom:6px;text-transform:uppercase;letter-spacing:.5px}
pre{background:#0d1117;padding:10px 14px;border-radius:4px;overflow-x:auto;font-size:13px;color:#7ee787;border:1px solid #21262d;white-space:pre-wrap;word-break:break-all}
pre.cmd{color:#d2a8ff}
.hint{color:#8b949e;font-size:12px;margin-top:6px}
.badge{font-size:11px;padding:1px 6px;border-radius:10px;margin-left:6px}
.badge-green{background:#0a4225;color:#7ee787}
.badge-yellow{background:#3b2700;color:#d2991d}
.badge-blue{background:#04244a;color:#79c0ff}
a{color:#58a6ff;text-decoration:none}a:hover{text-decoration:underline}
.steps{font-size:13px;color:#c9d1d9;line-height:2;padding-left:16px}
.footer{margin-top:24px;border-top:1px solid #30363d;padding-top:12px;font-size:12px;color:#484f58}
.footer a{color:#6e7681}
</style></head><body>
<h1>WG-Manager</h1>
<p class="sub">Server: ` + ip + `  |  Port: ` + p + `  |  Subnet: ` + wgSubnet + `</p>
<div class="tabs">
  <span class="tab active" onclick="show('linux')">Linux / macOS / WSL</span>
  <span class="tab" onclick="show('windows')">Windows</span>
  <span class="tab" onclick="show('mobile')">Mobile QR</span>
  <span class="tab" onclick="show('admin')">Admin</span>
</div>

<div id="linux" class="section active">
  <div class="box">
    <h3>Approval Mode <span class="badge badge-green">default</span></h3>
    <p class="hint">Submit a request, an admin approves it, then you auto-connect.</p>
    <pre>curl -sSf http://` + ip + `:` + p + `/connect | sudo bash</pre>
    <p class="hint">With custom name: append ?name=myname to the URL</p>
  </div>
  <div class="box">
    <h3>Direct Mode <span class="badge badge-yellow">requires API key</span></h3>
    <p class="hint">Trusted users join instantly with just one command.</p>
    <pre class="cmd">curl -sSf "http://` + ip + `:` + p + `/connect?mode=direct&name=my-laptop" | sudo bash</pre>
  </div>
  <div class="box">
    <h3>Verify</h3>
    <ol class="steps"><li><pre>sudo wg show</pre></li><li><pre>ping 10.0.0.1</pre></li></ol>
  </div>
</div>

<div id="windows" class="section">
  <div class="box">
    <h3>PowerShell — Approval</h3>
    <pre>Invoke-WebRequest http://` + ip + `:` + p + `/connect -OutFile join.ps1
.\join.ps1</pre>
    <p class="hint">Enter peer name when prompted. Wait for admin approval.</p>
  </div>
  <div class="box">
    <h3>PowerShell — Direct <span class="badge badge-yellow">API key embedded</span></h3>
    <pre class="cmd">Invoke-WebRequest "http://` + ip + `:` + p + `/connect?mode=direct&name=MYPC" -OutFile wg0.conf</pre>
  </div>
  <div class="box">
    <h3>CMD — Approval</h3>
    <pre>curl -X POST http://` + ip + `:` + p + `/api/v1/request -H "Content-Type: application/json" -d "{\"hostname\":\"MYPC\",\"dns\":\"1.1.1.1\"}"</pre>
  </div>
  <div class="box">
    <h3>CMD — Direct</h3>
    <pre class="cmd">curl -o wg0.conf "http://` + ip + `:` + p + `/connect?mode=direct&name=MYPC"</pre>
  </div>
  <div class="box">
    <h3>Import .conf into WireGuard</h3>
    <ol class="steps">
      <li>Install <a href="https://download.wireguard.com/windows-client/" target="_blank">WireGuard for Windows</a></li>
      <li>WireGuard → Import Tunnel(s) from file → select .conf</li>
      <li>Click Activate</li>
    </ol>
    <p class="hint">If ping fails: New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow</p>
  </div>
</div>

<div id="mobile" class="section">
  <div class="box">
    <h3>Generate QR on Server <span class="badge badge-yellow">direct only</span></h3>
    <p class="hint">Admin runs this on the server to create a QR image for a device (direct mode only):</p>
    <pre>curl -s "http://localhost:` + p + `/connect?qrcode&mode=direct&name=phone1" -o phone1.svg</pre>
    <p class="hint">Send phone1.svg to the mobile device → WireGuard App → Scan from QR code</p>
  </div>
  <div class="box">
    <h3>QR from Browser</h3>
    <p class="hint">Direct-registration QR (API key embedded, mobile only):</p>
    <pre>http://` + ip + `:` + p + `/connect?qrcode&mode=direct&name=phone1</pre>
    <p class="hint">Open in browser to see the QR, then scan with WireGuard app. For approval flow, use desktop /connect.</p>
  </div>
</div>

<div id="admin" class="section">
  <div class="box">
    <h3>Server Management</h3>
    <pre>wg-mgmt-tui                           # Interactive TUI dashboard
bash scripts/health-check.sh          # System health check
bash scripts/list-peers.sh            # List peers with status
tail -f /var/log/wg-mgmt/audit.log    # Audit log</pre>
  </div>
  <div class="box">
    <h3>API Reference</h3>
    <pre>GET  /api/v1/health                    Health check (no auth)
GET  /api/v1/peers                     List peers (server-local)
GET  /api/v1/requests                  Pending requests (server-local)
GET  /api/v1/status                    Daemon + WG status (server-local)
POST /api/v1/register                  Register peer (API key)
POST /api/v1/request                   Submit approval request (rate-limited)
POST /api/v1/requests/{id}/approve     Approve request (server-local)
DELETE /api/v1/peers/{name}            Remove peer (server-local)
DELETE /api/v1/requests/{id}           Reject request (server-local)</pre>
  </div>
</div>

<div class="footer">
  WG-Manager  |  <a href="/api/v1/health">health</a>  |  <a href="/api/v1/status">status</a>
</div>
<script>function show(id){document.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'));document.querySelectorAll('.section').forEach(s=>s.classList.remove('active'));document.getElementById(id).classList.add('active');event.target.classList.add('active')}</script>
</body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func (h *Handler) serveBootstrapHTML(w http.ResponseWriter, r *http.Request) {
	ip := h.cfg().ServerPublicIP
	wgSubnet := h.cfg().WGSubnet
	html := `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>WG-Manager - Join</title>
<style>*{margin:0;padding:0;box-sizing:border-box}
body{font:14px/1.6 system-ui,-apple-system,sans-serif;background:#0d1117;color:#c9d1d9;max-width:680px;margin:0 auto;padding:20px 16px}
h1{color:#58a6ff;font-size:20px;margin-bottom:2px}
.sub{color:#8b949e;font-size:12px;margin-bottom:20px}
.box{background:#161b22;border:1px solid #30363d;border-radius:6px;padding:16px;margin-bottom:14px}
.box h3{font-size:12px;color:#58a6ff;margin-bottom:8px;text-transform:uppercase;letter-spacing:.5px}
.box p{font-size:13px;color:#c9d1d9;margin-bottom:8px;line-height:1.5}
label{display:block;font-size:12px;color:#8b949e;margin-bottom:4px;margin-top:10px}
input{display:block;width:100%;padding:8px 10px;font-size:13px;font-family:monospace;background:#0d1117;color:#c9d1d9;border:1px solid #30363d;border-radius:4px;outline:none}
input:focus{border-color:#58a6ff}
button{display:block;width:100%;margin-top:12px;padding:10px;font-size:13px;font-weight:600;color:#fff;background:#238636;border:1px solid #2ea043;border-radius:4px;cursor:pointer}
button:hover{background:#2ea043}
pre{background:#0d1117;padding:10px 14px;border-radius:4px;overflow-x:auto;font-size:13px;color:#7ee787;border:1px solid #21262d;white-space:pre-wrap;word-break:break-all;margin-top:8px}
pre.cmd{color:#d2a8ff}
.hint{color:#8b949e;font-size:12px;margin-top:6px}
.hidden{display:none}
a{color:#58a6ff;text-decoration:none}a:hover{text-decoration:underline}
ol{font-size:13px;color:#c9d1d9;line-height:2;padding-left:16px;margin-top:4px}
.footer{margin-top:24px;border-top:1px solid #30363d;padding-top:12px;font-size:12px;color:#484f58}
#result{margin-top:12px;padding:10px;background:#0a3622;border:1px solid #2ea043;border-radius:4px}
#result.error{background:#3d1117;border-color:#f85149}
#result .title{font-size:12px;color:#7ee787;margin-bottom:4px;font-weight:600}
#result.error .title{color:#f85149}
</style></head><body>
<h1>WG-Manager</h1>
<p class="sub">Server: ` + ip + `  |  Subnet: ` + wgSubnet + `</p>
<div class="box">
  <h3>Join This Network</h3>
  <p>Onboarding uses <strong>one-time invite tokens</strong>. Your administrator creates an invite and shares a token with you. The token is consumed on first use — one token, one device.</p>
</div>
<div class="box">
  <h3>Paste Your Invite Token</h3>
  <label for="token">Invite Token (64 hex characters)</label>
  <input type="text" id="token" placeholder="Paste your invite token here" autocomplete="off" spellcheck="false">
  <label for="peer-name">Device Name (optional)</label>
  <input type="text" id="peer-name" placeholder="e.g. my-laptop" autocomplete="off" spellcheck="false">
  <button onclick="generateBootstrap()">Generate Bootstrap Command</button>
  <div id="result" class="hidden"></div>
</div>
<div class="box">
  <h3>Auto-Install (Linux / macOS / WSL)</h3>
  <p>Pipe the bootstrap script directly into bash. Always inspect first:</p>
  <pre class="cmd" id="inspect-cmd">curl -sSf https://` + ip + `/bootstrap</pre>
  <p class="hint">Replace TOKEN below and run:</p>
  <pre class="cmd" id="run-cmd">curl -sSf "https://` + ip + `/bootstrap?token=TOKEN&name=my-device" | sudo bash</pre>
</div>
<div class="box">
  <h3>Manual Redemption (All Platforms)</h3>
  <p>Redeem via the API and save the config manually:</p>
  <pre>curl -sSf -X POST https://` + ip + `/api/v1/redeem \
  -H "Content-Type: application/json" \
  -d '{"token":"YOUR_TOKEN","name":"my-device"}'</pre>
  <p class="hint">Windows PowerShell: <code>Invoke-RestMethod -Uri https://` + ip + `/api/v1/redeem -Method Post -Body (@{token="TOKEN";name="MYPC"} | ConvertTo-Json) -ContentType "application/json"</code></p>
</div>
<div class="box">
  <h3>After Redemption</h3>
  <ol>
    <li>Save the returned WireGuard config to a <code>.conf</code> file</li>
    <li>Import into <a href="https://www.wireguard.com/install/" target="_blank">WireGuard client</a></li>
    <li>Activate the tunnel</li>
    <li>Ping <code>` + h.cfg().WGServerIP + `</code> to verify</li>
  </ol>
</div>
<div class="footer">
  WG-Manager  |  <a href="/api/v1/health">health</a>  |  <a href="/api/v1/status">status</a>
</div>
<script>
function generateBootstrap() {
  var token = document.getElementById('token').value.trim();
  var name = document.getElementById('peer-name').value.trim() || 'my-device';
  var result = document.getElementById('result');
  result.classList.remove('error');
  if (!token) {
    result.className = 'hidden error';
    result.innerHTML = '<div class="title">Missing Token</div><div>Please paste your invite token.</div>';
    result.classList.remove('hidden');
    return;
  }
  var url = 'https://` + ip + `/bootstrap?token=' + encodeURIComponent(token) + '&name=' + encodeURIComponent(name);
  result.className = '';
  result.innerHTML = '<div class="title">Bootstrap Command</div><pre style="margin-top:4px;word-break:break-all">curl -sSf "' + url + '" | sudo bash</pre><div class="hint">Tip: run <code>curl -sSf "' + url + '"</code> first to inspect the script before piping to bash.</div>';
  result.classList.remove('hidden');
}
</script>
</body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

// ServeInviteQR generates an SVG QR code that encodes an invite bootstrap URL.
// Access: LocalOnly + RequireRole(admin, owner).
// Query params: ?token=RAW_TOKEN&name=DEVICE_NAME.
// The raw token is verified by hashing it and checking against the invite store.
// The QR encodes: https://SERVER_HOST/bootstrap?token=RAW_TOKEN&name=DEVICE_NAME
func (h *Handler) ServeInviteQR(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token query parameter is required"})
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "mobile"
	}
	if err := validatePeerName(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Verify the token is valid by checking its hash against the store.
	tokenSum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(tokenSum[:])

	inv, ok := h.store.GetInviteByTokenHash(tokenHash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite not found for the given token"})
		return
	}

	if inv.Status != store.InviteCreated {
		writeJSON(w, http.StatusGone, map[string]string{
			"error":  fmt.Sprintf("invite is already %s", inv.Status),
			"status": string(inv.Status),
		})
		return
	}

	if inv.ExpiresAt != "" {
		expAt, err := time.Parse(time.RFC3339, inv.ExpiresAt)
		if err == nil && time.Now().UTC().After(expAt) {
			writeJSON(w, http.StatusGone, map[string]string{"error": "invite has expired"})
			return
		}
	}

	// Build the bootstrap URL — uses HTTPS reverse-proxy port, not the raw daemon port.
	serverHost := h.cfg().ServerPublicIP
	bootstrapURL := fmt.Sprintf("https://%s/bootstrap?token=%s&name=%s", serverHost, token, name)

	svg := generateQR(bootstrapURL)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(svg))
}

func (h *Handler) SubmitRequest(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "use invite redemption via POST /api/v1/redeem")
}

func (h *Handler) RequestStatus(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "use invite redemption via POST /api/v1/redeem")
}

func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "approval flow deprecated; use invite-based onboarding: GET /api/v1/invites")
}

func (h *Handler) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "approval flow deprecated; use invite-based onboarding: POST /api/v1/invites")
}

func (h *Handler) RejectRequest(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "approval flow deprecated; use invite-based onboarding")
}

func (h *Handler) ManageRequest(w http.ResponseWriter, r *http.Request) {
	writeDeprecated(w, "approval flow deprecated; use invite-based onboarding")
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) hasPendingRequest(hostname string) bool {
	for _, rq := range h.store.PendingRequests() {
		if rq.Hostname == hostname {
			return true
		}
	}
	return false
}

func auditFields(pairs ...string) map[string]string {
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func getTokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func tokenHashFromBearer(r *http.Request) (string, bool) {
	token := getTokenFromRequest(r)
	if token == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), true
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and password are required"})
		return
	}

	user, ok := h.store.AuthenticateUser(req.Name, req.Password)
	if !ok {
		src := remoteIP(r)
		audit.Log("login_failed", auditFields("name", req.Name, "source", src, "reason", "invalid_credentials"))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	token, err := h.store.CreateSession(user.Name, user.Role, 24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}

	src := remoteIP(r)
	audit.Log("login_success", auditFields("name", user.Name, "source", src))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"role":  string(user.Role),
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	tokenHash, ok := tokenHashFromBearer(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid Authorization header"})
		return
	}

	if err := h.store.DeleteSession(tokenHash); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	src := remoteIP(r)
	audit.Log("logout", auditFields("source", src))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func (h *Handler) RedeemInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		DNS   string `json:"dns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if err := validatePeerName(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}

	tokenSum := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(tokenSum[:])

	// Resolve DNS default first — invite-specific override applied after redeem.
	dns := req.DNS
	if dns == "" {
		dns = h.cfg().DefaultDNS
	}
	if err := validateDNS(dns); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 1. Atomically redeem the invite BEFORE creating any peer resources.
	// This prevents the double-redeem race: if provisioning fails later,
	// the invite is consumed but no orphan peer exists.
	redeemedInv, err := h.store.RedeemInviteByTokenHash(tokenHash, req.Name)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	// Apply invite-level DNS override if user didn't specify one.
	if req.DNS == "" && redeemedInv.DNSOverride != "" {
		dns = redeemedInv.DNSOverride
	}

	// 2. Generate WireGuard key pair for the new peer.
	privateKey, publicKey, err := h.wgMgr.GenKeyPair()
	if err != nil {
		h.store.UnredeemByTokenHash(tokenHash)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
		return
	}

	peer := store.Peer{
		Name:       req.Name,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		DNS:        dns,
		Keepalive:  h.cfg().PeerKeepalive,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// 3. Allocate IP and add peer to the store.
	_, err = h.store.AllocateIPAndAddPeer(&peer, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
	if err != nil {
		h.store.UnredeemByTokenHash(tokenHash)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	// 4. Commit peer to WireGuard: add live peer, write config, persist state.
	// commitPeerToWG handles its own rollback (removes peer from store on failure).
	if err := h.commitPeerToWG(peer.Name); err != nil {
		h.store.UnredeemByTokenHash(tokenHash)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	src := remoteIP(r)
	audit.Log("invite_redeemed", auditFields("name", peer.Name, "ip", peer.Address, "source", src, "invite_id", redeemedInv.ID))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"peer": map[string]interface{}{
			"name":              peer.Name,
			"address":           fmt.Sprintf("%s/24", peer.Address),
			"private_key":       peer.PrivateKey,
			"public_key":        peer.PublicKey,
			"server_public_key": h.store.Server().PublicKey,
			"server_endpoint":   h.cfg().ServerEndpoint(),
			"dns":               peer.DNS,
			"keepalive":         peer.Keepalive,
		},
	})
}

// ── Invite Management Handlers ─────────────────────────────────────────

func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		NameHint string `json:"name_hint"`
		DNS      string `json:"dns"`
		TTLHours int    `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.NameHint = strings.TrimSpace(req.NameHint)
	req.DNS = strings.TrimSpace(req.DNS)

	var ttl time.Duration
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
	} else {
		ttl = 72 * time.Hour // default 3 days
	}

	// Determine the issuer from context (set by RequireRole middleware).
	issuedBy := "apikey"
	if user, ok := r.Context().Value(ContextKeyUser).(string); ok && user != "apikey" {
		issuedBy = user
	}

	var opts []store.InviteOption
	if req.NameHint != "" {
		opts = append(opts, store.WithDisplayNameHint(req.NameHint))
	}
	if req.DNS != "" {
		if err := validateDNS(req.DNS); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		opts = append(opts, store.WithDNSOverride(req.DNS))
	}

	rawToken, inv, err := h.store.CreateInvite(issuedBy, ttl, opts...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("invite_created", auditFields("invite_id", inv.ID, "issued_by", issuedBy))

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"invite_id":  inv.ID,
		"token":      rawToken,
		"expires_at": inv.ExpiresAt,
		"message":    "Share this token with the client. It will only be shown once.",
	})
}

func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Clean up expired invites.
	_ = h.store.ExpireInvites()

	invites := h.store.ListInvites()

	type inviteInfo struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		CreatedAt       string `json:"created_at"`
		ExpiresAt       string `json:"expires_at,omitempty"`
		RedeemedAt      string `json:"redeemed_at,omitempty"`
		RedeemedBy      string `json:"redeemed_by,omitempty"`
		RevokedAt       string `json:"revoked_at,omitempty"`
		IssuedBy        string `json:"issued_by"`
		DisplayNameHint string `json:"display_name_hint,omitempty"`
		DNSOverride     string `json:"dns_override,omitempty"`
	}

	result := make([]inviteInfo, 0, len(invites))
	for _, inv := range invites {
		result = append(result, inviteInfo{
			ID:              inv.ID,
			Status:          string(inv.Status),
			CreatedAt:       inv.CreatedAt,
			ExpiresAt:       inv.ExpiresAt,
			RedeemedAt:      inv.RedeemedAt,
			RedeemedBy:      inv.RedeemedBy,
			RevokedAt:       inv.RevokedAt,
			IssuedBy:        inv.IssuedBy,
			DisplayNameHint: inv.DisplayNameHint,
			DNSOverride:     inv.DNSOverride,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invite_count": len(result),
		"invites":      result,
	})
}

func (h *Handler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/invites/")
	if id == "" || id == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite_id is required"})
		return
	}

	if err := h.store.RevokeInvite(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("invite_revoked", auditFields("invite_id", id))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("invite %q revoked", id),
	})
}

// ── User Management Handlers ───────────────────────────────────────────

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	users := h.store.ListUsers()

	type userInfo struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		CreatedAt string `json:"created_at"`
	}

	result := make([]userInfo, 0, len(users))
	for _, u := range users {
		result = append(result, userInfo{
			Name:      u.Name,
			Role:      string(u.Role),
			CreatedAt: u.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_count": len(result),
		"users":      result,
	})
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and password are required"})
		return
	}

	role := store.RoleUser
	switch req.Role {
	case string(store.RoleOwner):
		role = store.RoleOwner
	case string(store.RoleAdmin):
		role = store.RoleAdmin
	case string(store.RoleUser), "":
		role = store.RoleUser
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":       "invalid role",
			"valid_roles": "owner, admin, user",
		})
		return
	}

	passwordHash, err := store.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		return
	}

	if err := h.store.AddUser(req.Name, passwordHash, role); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("user_created", auditFields("name", req.Name, "role", string(role)))

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"name": req.Name,
			"role": string(role),
		},
	})
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	if name == "" || name == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user name is required"})
		return
	}

	if err := h.store.DeleteUser(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("user_deleted", auditFields("name", name))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("user %q deleted", name),
	})
}

func (h *Handler) ListPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	peers := h.store.AllPeers()

	wgStatus, err := h.wgShowCached(h.cfg().WGInterface)
	wgPeers := make(map[string]wg.PeerStatus)
	if err == nil {
		for _, p := range wgStatus.Peers {
			wgPeers[p.PublicKey] = p
		}
	}

	type peerInfo struct {
		Name            string `json:"name"`
		PublicKey       string `json:"public_key"`
		Address         string `json:"address"`
		DNS             string `json:"dns,omitempty"`
		Keepalive       int    `json:"keepalive"`
		CreatedAt       string `json:"created_at,omitempty"`
		Endpoint        string `json:"endpoint,omitempty"`
		LatestHandshake string `json:"latest_handshake,omitempty"`
		TransferRx      string `json:"transfer_rx,omitempty"`
		TransferTx      string `json:"transfer_tx,omitempty"`
		Online          bool   `json:"online"`
		Orphaned        bool   `json:"orphaned,omitempty"`
	}

	seenPubKeys := make(map[string]bool)
	result := make([]peerInfo, 0, len(peers)+len(wgPeers))
	for _, p := range peers {
		pi := peerInfo{
			Name:      p.Name,
			PublicKey: p.PublicKey,
			Address:   p.Address,
			DNS:       p.DNS,
			Keepalive: p.Keepalive,
			CreatedAt: p.CreatedAt,
		}
		if ws, ok := wgPeers[p.PublicKey]; ok {
			pi.Endpoint = ws.Endpoint
			pi.LatestHandshake = ws.LatestHandshake
			pi.TransferRx = ws.TransferRx
			pi.TransferTx = ws.TransferTx
			pi.Online = ws.LatestHandshake != "0"
		}
		seenPubKeys[p.PublicKey] = true
		result = append(result, pi)
	}

	for pubKey, ws := range wgPeers {
		if !seenPubKeys[pubKey] {
			ip := "0.0.0.0"
			if parts := strings.SplitN(ws.AllowedIPs, "/", 2); parts[0] != "" {
				ip = parts[0]
			}
			result = append(result, peerInfo{
				Name:            "(orphan)",
				PublicKey:       pubKey,
				Address:         ip,
				Endpoint:        ws.Endpoint,
				LatestHandshake: ws.LatestHandshake,
				TransferRx:      ws.TransferRx,
				TransferTx:      ws.TransferTx,
				Online:          ws.LatestHandshake != "0",
				Orphaned:        true,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server_endpoint": h.cfg().ServerEndpoint(),
		"peer_count":      len(result),
		"peers":           result,
	})
}

func (h *Handler) DeletePeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/v1/peers/")
	if name == "" || name == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peer name is required"})
		return
	}

	peer, ok := h.store.GetPeer(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("peer %q not found", name),
		})
		return
	}

	if err := h.wgMgr.RemovePeerByKey(h.cfg().WGInterface, peer.PublicKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from wireguard"})
		return
	}

	if err := h.store.RemovePeer(name); err != nil {
		log.Printf("ROLLBACK: re-adding peer %q to WG after store remove failure: %v", name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from store"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		h.store.AddPeer(peer)
		log.Printf("ROLLBACK: re-adding peer %q to WG after config write failure: %v", name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		h.store.AddPeer(peer)
		log.Printf("ROLLBACK: re-adding peer %q to WG after state save failure: %v", name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("peer_deleted", auditFields("name", peer.Name, "ip", peer.Address, "admin", remoteIP(r)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("peer %q removed", name),
	})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	wgStatus, err := h.wgShowCached(h.cfg().WGInterface)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"interface":  h.cfg().WGInterface,
			"daemon":     "running",
			"wireguard":  "error",
			"wg_error":   err.Error(),
			"peer_count": len(h.store.AllPeers()),
		})
		return
	}

	onlineCount := 0
	for _, p := range wgStatus.Peers {
		if p.LatestHandshake != "0" {
			onlineCount++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"interface":   h.cfg().WGInterface,
		"listen_port": wgStatus.ListenPort,
		"daemon":      "running",
		"wireguard":   "ok",
		"peer_online": onlineCount,
		"peer_total":  len(h.store.AllPeers()),
	})
}

// Bootstrap serves a self-contained bash script that onboards a peer using an invite token.
// The script is inspectable before execution, installs WireGuard if needed, redeems the
// invite via POST /api/v1/redeem, saves the returned config, and starts WireGuard.
// It contains NO global API key — the invite token is the sole credential.
func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	name := r.URL.Query().Get("name")

	serverHost := h.cfg().ServerPublicIP

	script := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
# WG-Manager — Invite Bootstrap Script
# Served by /bootstrap — inspect before running: curl -sSf https://%s/bootstrap
# Usage: curl -sSf "https://%s/bootstrap?token=INVITE_TOKEN&name=MYDEVICE" | sudo bash

SERVER_HOST="%s"
INVITE_TOKEN="%s"
PEER_NAME="%s"
DEFAULT_DNS="%s"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; }

if [ "${INVITE_TOKEN}" = "" ]; then
    err "Missing invite token. Usage: curl -sSf \"https://$SERVER_HOST/bootstrap?token=INVITE_TOKEN&name=MYDEVICE\" | sudo bash"
    exit 1
fi

# ── OS detection ──
detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "macos" ;;
        *)       echo "unknown" ;;
    esac
}
OS=$(detect_os)

# ── Install WireGuard ──
install_wg() {
    if command -v wg &>/dev/null; then
        log "WireGuard is already installed"
        return 0
    fi
    log "Installing WireGuard..."
    case "$OS" in
        linux)
            if command -v apt-get &>/dev/null; then
                apt-get update -qq && apt-get install -y -qq wireguard-tools
            elif command -v yum &>/dev/null; then
                yum install -y wireguard-tools
            elif command -v dnf &>/dev/null; then
                dnf install -y wireguard-tools
            elif command -v apk &>/dev/null; then
                apk add wireguard-tools
            else
                err "Cannot install WireGuard — unknown package manager"
                exit 1
            fi
            ;;
        macos)
            if command -v brew &>/dev/null; then
                brew install wireguard-tools
            else
                warn "Install Homebrew first: https://brew.sh"
                exit 1
            fi
            ;;
        *)
            err "Unsupported OS: $OS"
            exit 1
            ;;
    esac
    log "WireGuard installed successfully"
}

# ── JSON parsing without jq ──
json_get() {
    local json="$1" key="$2" default="${3:-}"
    if command -v jq &>/dev/null; then
        echo "$json" | jq -r ".$key" 2>/dev/null || echo "$default"
    elif command -v python3 &>/dev/null; then
        echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$key','$default'))" 2>/dev/null || echo "$default"
    else
        echo "$default"
    fi
}

json_get_nested() {
    local json="$1" key1="$2" key2="$3" default="${4:-}"
    if command -v jq &>/dev/null; then
        echo "$json" | jq -r ".${key1}.${key2}" 2>/dev/null || echo "$default"
    elif command -v python3 &>/dev/null; then
        echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$key1',{}).get('$key2','$default'))" 2>/dev/null || echo "$default"
    else
        echo "$default"
    fi
}

# ── Auto-sudo ──
auto_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif command -v sudo &>/dev/null; then
        sudo "$@"
    else
        err "This script needs root privileges. Please run with sudo."
        exit 1
    fi
}

# ── Main ──
log "WG-Manager Invite Bootstrap"
log "OS: $OS"

if [ "${PEER_NAME}" = "" ]; then
    PEER_NAME=$(hostname 2>/dev/null || echo "wg-peer")
    log "No peer name provided, using hostname: $PEER_NAME"
fi

install_wg

log "Redeeming invite token..."
RESP=$(curl -sSf -X POST "https://$SERVER_HOST/api/v1/redeem" \
    -H "Content-Type: application/json" \
    -d "{\"token\":\"$INVITE_TOKEN\",\"name\":\"$PEER_NAME\",\"dns\":\"$DEFAULT_DNS\"}")

SUCCESS=$(json_get "$RESP" "success" "")
if [ "$SUCCESS" != "true" ]; then
    ERR_MSG=$(json_get "$RESP" "error" "unknown error")
    err "Failed to redeem invite: $ERR_MSG"
    exit 1
fi

log "Invite redeemed successfully!"

PRIVATE_KEY=$(json_get_nested "$RESP" "peer" "private_key")
ADDRESS=$(json_get_nested "$RESP" "peer" "address")
SERVER_PUBKEY=$(json_get_nested "$RESP" "peer" "server_public_key")
SERVER_ENDPOINT=$(json_get_nested "$RESP" "peer" "server_endpoint")
DNS=$(json_get_nested "$RESP" "peer" "dns" "$DEFAULT_DNS")
KEEPALIVE=$(json_get_nested "$RESP" "peer" "keepalive" "25")

# ── Write WireGuard config ──
WG_CONF="/etc/wireguard/wg0.conf"
log "Writing WireGuard config to $WG_CONF..."
auto_sudo mkdir -p /etc/wireguard
auto_sudo bash -c "cat > $WG_CONF" << WGCONF
[Interface]
Address = $ADDRESS
PrivateKey = $PRIVATE_KEY
DNS = $DNS

[Peer]
PublicKey = $SERVER_PUBKEY
Endpoint = $SERVER_ENDPOINT
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = $KEEPALIVE
WGCONF
auto_sudo chmod 600 "$WG_CONF"

# ── Start WireGuard ──
log "Starting WireGuard..."
if command -v systemctl &>/dev/null && auto_sudo systemctl is-active --quiet wg-quick@wg0 2>/dev/null; then
    auto_sudo wg-quick down wg0 2>/dev/null || true
fi
auto_sudo wg-quick up wg0

# ── Verify ──
sleep 2
if auto_sudo wg show wg0 &>/dev/null; then
    PEER_IP=$(echo "$ADDRESS" | cut -d/ -f1)
    log "WireGuard is active — your VPN IP: $PEER_IP"
    log "Try: ping %s"
else
    warn "WireGuard may not have started correctly. Check: sudo wg show"
fi

log "Done!"
`, serverHost, serverHost,
		serverHost, token, name,
		h.cfg().DefaultDNS, h.cfg().WGServerIP)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *Handler) writeConfigToDisk() error {
	peers := h.store.AllPeers()
	peerMap := make(map[string]wg.PeerInfo)
	for _, p := range peers {
		peerMap[p.Name] = wg.PeerInfo{
			PubKey:    p.PublicKey,
			Address:   p.Address,
			Keepalive: p.Keepalive,
		}
	}
	return wg.WriteFullConfig(
		h.cfg().WGConfPath,
		h.cfg().WGInterface,
		h.cfg().WGPort,
		h.cfg().PeerKeepalive,
		h.cfg().WGServerIP,
		h.store.Server().PrivateKey,
		peerMap,
	)
}

func (h *Handler) commitPeerToWG(name string) error {
	peer, ok := h.store.GetPeer(name)
	if !ok {
		return fmt.Errorf("peer %q not found in store", name)
	}

	allowedIP := fmt.Sprintf("%s/32", peer.Address)

	if err := h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, allowedIP, peer.Keepalive); err != nil {
		h.store.RemovePeer(name)
		return fmt.Errorf("failed to add peer to wireguard: %w", err)
	}

	if err := h.writeConfigToDisk(); err != nil {
		if rbErr := h.wgMgr.RemovePeerByKey(h.cfg().WGInterface, peer.PublicKey); rbErr != nil {
			log.Printf("ROLLBACK: failed to remove peer %q from WG after config write failure: %v", name, rbErr)
		}
		h.store.RemovePeer(name)
		return fmt.Errorf("failed to write config: %w", err)
	}

	if err := h.store.Save(); err != nil {
		if rbErr := h.wgMgr.RemovePeerByKey(h.cfg().WGInterface, peer.PublicKey); rbErr != nil {
			log.Printf("ROLLBACK: failed to remove peer %q from WG after state save failure: %v", name, rbErr)
		}
		h.store.RemovePeer(name)
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

func portStr(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil { return "58880" }
	if n, _ := strconv.Atoi(p); n > 0 { return p }
	return "58880"
}

func writeDeprecated(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusGone, map[string]string{
		"error":   "endpoint deprecated",
		"message": msg,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	result := bytes.TrimRight(buf.Bytes(), "\n")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(result)))
	w.WriteHeader(status)
	w.Write(result)
}

func validatePeerName(name string) error {
	if len(name) > 64 {
		return fmt.Errorf("peer name too long (max 64 characters)")
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
			return fmt.Errorf("peer name contains invalid characters (use a-z, A-Z, 0-9, -)")
		}
	}
	return nil
}

func validateDNS(dns string) error {
	if dns == "" {
		return nil
	}
	for _, addr := range strings.Split(dns, ",") {
		addr = strings.TrimSpace(addr)
		if net.ParseIP(addr) == nil {
			return fmt.Errorf("invalid DNS server: %q (must be an IP address)", addr)
		}
	}
	return nil
}

func (h *Handler) getWGPeerIPs(excludePubKey string) map[string]bool {
	ips := make(map[string]bool)
	wgStatus, err := h.wgMgr.Show(h.cfg().WGInterface)
	if err != nil {
		return ips
	}
	for _, p := range wgStatus.Peers {
		if p.PublicKey == excludePubKey {
			continue
		}
		if parts := strings.SplitN(p.AllowedIPs, "/", 2); parts[0] != "" && parts[0] != "0.0.0.0" {
			ips[parts[0]] = true
		}
	}
	return ips
}
