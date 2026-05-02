package api

import (
	"bytes"
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
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Hostname string `json:"hostname"`
		DNS      string `json:"dns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Hostname = strings.TrimSpace(req.Hostname)
	if req.Hostname == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hostname is required"})
		return
	}
	if err := validatePeerName(req.Hostname); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.store.HasPeer(req.Hostname) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("peer %q already exists", req.Hostname),
			"hint":  "contact admin to remove the existing peer first",
		})
		return
	}

	dns := req.DNS
	if dns == "" {
		dns = h.cfg().DefaultDNS
	}
	if err := validateDNS(dns); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	privateKey, publicKey, err := h.wgMgr.GenKeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
		return
	}

	peer := store.Peer{
		Name:       req.Hostname,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		DNS:        dns,
		Keepalive:  h.cfg().PeerKeepalive,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	_, err = h.store.AllocateIPAndAddPeer(&peer, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no available IP addresses"})
		return
	}

	if err := h.commitPeerToWG(peer.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	audit.Log("peer_registered", auditFields("name", peer.Name, "ip", peer.Address, "source", remoteIP(r)))

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

func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	ua := r.Header.Get("User-Agent")
	mode := r.URL.Query().Get("mode")
	name := r.URL.Query().Get("name")
	platform := r.URL.Query().Get("platform")

	// QR code endpoint
	if _, ok := r.URL.Query()["qrcode"]; ok {
		h.serveQR(w, r, mode, name)
		return
	}

	isPS := platform == "windows" || strings.Contains(ua, "PowerShell") || strings.Contains(ua, "WindowsPowerShell")
	isShell := platform == "linux" || platform == "wsl" || platform == "macos" ||
		strings.Contains(ua, "curl") || strings.Contains(ua, "Wget") || strings.Contains(ua, "bash") || strings.Contains(ua, "libcurl")
	isBrowser := (strings.Contains(ua, "Mozilla") || strings.Contains(ua, "Chrome")) &&
		!strings.Contains(ua, "curl") && !strings.Contains(ua, "PowerShell") && !strings.Contains(ua, "Wget")

	if !isPS && !isShell && !isBrowser {
		isShell = true
	}

	isDirect := mode == "direct"

	if isBrowser {
		h.serveHTML(w, r)
		return
	}

	if isPS {
		if isDirect {
			h.serveDirectWin(w, r, name)
		} else {
			h.serveApprovalPS1(w, r)
		}
		return
	}

	if isDirect {
		h.serveDirectBash(w, r, name)
	} else {
		h.serveApprovalBash(w, r, name)
	}
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

func (h *Handler) serveQR(w http.ResponseWriter, r *http.Request, mode, name string) {
	isDirect := mode == "direct"

	if !isDirect {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "QR codes are only available in direct mode",
			"message": "Use mode=direct to generate a WireGuard config QR. For approval flow, open /connect in a browser.",
			"hint":    fmt.Sprintf("http://%s:%s/connect", h.cfg().ServerPublicIP, portStr(h.cfg().MgmtListen)),
		})
		return
	}

	if name == "" {
		name = "mobile"
	}
	q := r.URL.Query()
	dns := q.Get("dns")
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
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "keygen failed"})
			return
		}
		peer := store.Peer{
			Name: name, PublicKey: publicKey, PrivateKey: privateKey,
			DNS: dns, Keepalive: h.cfg().PeerKeepalive,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		ip, err = h.store.AllocateIPAndAddPeer(&peer, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no IP available"})
			return
		}
		if err := h.commitPeerToWG(peer.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		audit.Log("peer_registered", auditFields("name", name, "ip", ip, "source", "qr"))
	}

	content := fmt.Sprintf(`[Interface]
Address = %s/24
PrivateKey = %s
DNS = %s

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s
PersistentKeepalive = %d
`, ip, privateKey, dns, h.store.Server().PublicKey, h.cfg().ServerEndpoint(), h.cfg().WGSubnet, h.cfg().PeerKeepalive)

	svg := generateQR(content)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(svg))
}

func (h *Handler) SubmitRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Hostname string `json:"hostname"`
		DNS      string `json:"dns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Hostname = strings.TrimSpace(req.Hostname)
	if req.Hostname == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hostname is required"})
		return
	}
	if err := validatePeerName(req.Hostname); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.store.HasPeer(req.Hostname) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "peer already exists"})
		return
	}

	if h.hasPendingRequest(req.Hostname) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a pending request for this hostname already exists"})
		return
	}

	// Clean up expired requests
	expired := h.store.ExpireRequests()
	for _, e := range expired {
		audit.Log("request_expired", auditFields("name", e.Hostname, "ip", e.Address, "request_id", e.ID))
	}

	dns := req.DNS
	if dns == "" {
		dns = h.cfg().DefaultDNS
	}
	if err := validateDNS(dns); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	privateKey, publicKey, err := h.wgMgr.GenKeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
		return
	}

	requestID := store.GenerateRequestID()
	sourceIP := remoteIP(r)

	pendingReq := store.Request{
		ID:         requestID,
		Hostname:   req.Hostname,
		DNS:        dns,
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		Keepalive:  h.cfg().PeerKeepalive,
		SourceIP:   sourceIP,
	}

	ip, err := h.store.ReserveIPAndAddRequest(pendingReq, h.cfg().WGSubnet)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save request"})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("request_submitted", auditFields("name", req.Hostname, "ip", ip, "source", sourceIP, "request_id", requestID))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "pending",
		"request_id": requestID,
		"message":    "Request submitted. Waiting for admin approval.",
	})
}

func (h *Handler) RequestStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/request/")
	if id == "" || id == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
		return
	}

	// Check if request exists
	req, ok := h.store.GetRequest(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "not_found", "error": "request not found"})
		return
	}

	switch req.Status {
	case "approved":
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "approved",
			"request_id": id,
			"hostname":   req.Hostname,
			"peer": map[string]interface{}{
				"name":              req.Hostname,
				"address":           fmt.Sprintf("%s/24", req.Address),
				"private_key":       req.PrivateKey,
				"server_public_key": h.store.Server().PublicKey,
				"server_endpoint":   h.cfg().ServerEndpoint(),
				"dns":               req.DNS,
				"keepalive":         req.Keepalive,
			},
		})
		return
	case "rejected":
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "rejected",
			"request_id": id,
			"hostname":   req.Hostname,
			"message":    "Your request was rejected by the admin.",
		})
		return
	}

	// Check if expired
	expAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err == nil && time.Now().UTC().After(expAt) {
		writeJSON(w, http.StatusGone, map[string]string{"status": "expired", "error": "request has expired"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "pending",
		"request_id": id,
		"hostname":   req.Hostname,
		"message":    "Waiting for admin approval.",
	})
}

func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	expired := h.store.ExpireRequests()
	for _, e := range expired {
		audit.Log("request_expired", auditFields("name", e.Hostname, "ip", e.Address, "request_id", e.ID))
	}

	reqs := h.store.PendingRequests()

	type reqInfo struct {
		ID        string `json:"id"`
		Hostname  string `json:"hostname"`
		Address   string `json:"address"`
		DNS       string `json:"dns"`
		SourceIP  string `json:"source_ip"`
		CreatedAt string `json:"created_at"`
		ExpiresAt string `json:"expires_at"`
	}

	result := make([]reqInfo, 0, len(reqs))
	for _, rq := range reqs {
		result = append(result, reqInfo{
			ID:        rq.ID,
			Hostname:  rq.Hostname,
			Address:   rq.Address,
			DNS:       rq.DNS,
			SourceIP:  rq.SourceIP,
			CreatedAt: rq.CreatedAt,
			ExpiresAt: rq.ExpiresAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pending_count": len(result),
		"requests":      result,
	})
}

func (h *Handler) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/requests/")
	id = strings.TrimSuffix(id, "/approve")
	if id == "" || id == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
		return
	}

	peer, err := h.store.ApproveRequest(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if err := h.commitPeerToWG(peer.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	adminIP := remoteIP(r)
	audit.Log("request_approved", auditFields("name", peer.Name, "ip", peer.Address, "admin", adminIP, "request_id", id))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"peer": map[string]interface{}{
			"name":            peer.Name,
			"address":         fmt.Sprintf("%s/24", peer.Address),
			"private_key":     peer.PrivateKey,
			"public_key":      peer.PublicKey,
			"server_public_key": h.store.Server().PublicKey,
			"server_endpoint": h.cfg().ServerEndpoint(),
			"dns":             peer.DNS,
			"keepalive":       peer.Keepalive,
		},
	})
}

func (h *Handler) RejectRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/requests/")
	if id == "" || id == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
		return
	}

	rejected, err := h.store.RejectRequest(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	adminIP := remoteIP(r)
	audit.Log("request_rejected", auditFields("name", rejected.Hostname, "ip", rejected.Address, "admin", adminIP, "request_id", id))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("request %q rejected", rejected.Hostname),
	})
}

func (h *Handler) ManageRequest(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/approve") {
		h.ApproveRequest(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		h.RejectRequest(w, r)
		return
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST .../approve or DELETE"})
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
