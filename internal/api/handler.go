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
	"net/url"
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
	ServerHost     string
	DefaultDNS     string
	PeerKeepalive  int
	PeersDBPath    string
	WGConfPath     string
}

func (c *Config) ServerEndpoint() string {
	return fmt.Sprintf("%s:%d", c.ServerPublicIP, c.WGPort)
}

func (c *Config) PublicHost() string {
	if c.ServerHost != "" {
		return c.ServerHost
	}
	return c.ServerPublicIP
}

func (c *Config) PublicURL() string {
	host := c.PublicHost()
	if host == "" {
		return ""
	}
	// Domain hostnames (non-IP) use https; raw IP addresses use http.
	if net.ParseIP(host) == nil {
		return "https://" + host
	}
	return "http://" + host
}

type Handler struct {
	store   *store.State
	wgMgr   *wg.Manager
	config_ atomic.Pointer[Config]
	wgCache atomic.Pointer[wgCacheEntry]
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

func (h *Handler) serveBootstrapHTML(w http.ResponseWriter, r *http.Request) {
	publicURL := h.cfg().PublicURL()
	publicHost := h.cfg().PublicHost()
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
<p class="sub">Server: ` + publicHost + `  |  Subnet: ` + wgSubnet + `</p>
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
  <pre class="cmd" id="inspect-cmd">curl -sSf ` + publicURL + `/bootstrap</pre>
  <p class="hint">Replace TOKEN below and run:</p>
  <pre class="cmd" id="run-cmd">curl -sSf "` + publicURL + `/bootstrap?token=TOKEN&name=my-device" | sudo bash</pre>
</div>
<div class="box">
  <h3>Manual Redemption (All Platforms)</h3>
  <p>Redeem via the API and save the config manually:</p>
  <pre>curl -sSf -X POST ` + publicURL + `/api/v1/redeem \
  -H "Content-Type: application/json" \
  -d '{"token":"YOUR_TOKEN","name":"my-device"}'</pre>
  <p class="hint">Windows PowerShell: <code>Invoke-RestMethod -Uri ` + publicURL + `/api/v1/redeem -Method Post -Body (@{token="TOKEN";name="MYPC"} | ConvertTo-Json) -ContentType "application/json"</code></p>
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
  var url = '` + publicURL + `/bootstrap?token=' + encodeURIComponent(token) + '&name=' + encodeURIComponent(name);
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
// The QR encodes: PUBLIC_URL/bootstrap?token=RAW_TOKEN&name=DEVICE_NAME
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

	// Build the bootstrap URL using the canonical public URL helper.
	publicURL := h.cfg().PublicURL()
	bootstrapURL := fmt.Sprintf("%s/bootstrap?token=%s&name=%s", publicURL, token, name)

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

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	authUser, _ := r.Context().Value(ContextKeyUser).(string)
	if authUser == "" || authUser == "apikey" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve session"})
		return
	}

	tokenHash, ok := tokenHashFromBearer(r)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve session"})
		return
	}

	sess, ok := h.store.GetSessionByTokenHash(tokenHash)
	if !ok || sess.UserName != authUser {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve session"})
		return
	}

	user, ok := h.store.GetUser(sess.UserName)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve user"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":       user.Name,
		"role":       string(user.Role),
		"created_at": user.CreatedAt,
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
	if redeemedInv.PoolName != "" {
		pool, ok := h.store.GetPool(redeemedInv.PoolName)
		if !ok {
			h.store.UnredeemByTokenHash(tokenHash)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pool not found: " + redeemedInv.PoolName})
			return
		}
		ip, err := h.store.AllocateIPInPool(pool, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
		if err != nil {
			h.store.UnredeemByTokenHash(tokenHash)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		peer.Address = ip
		if err := h.store.AddPeer(peer); err != nil {
			h.store.UnredeemByTokenHash(tokenHash)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	} else {
		_, err = h.store.AllocateIPAndAddPeer(&peer, h.cfg().WGSubnet, h.getWGPeerIPs(publicKey))
		if err != nil {
			h.store.UnredeemByTokenHash(tokenHash)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
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
		NameHint   string            `json:"name_hint"`
		DNS        string            `json:"dns"`
		TTLHours   int               `json:"ttl_hours"`
		PoolName   string            `json:"pool_name"`
		TargetRole string            `json:"target_role"`
		DeviceName string            `json:"device_name"`
		MaxUses    int               `json:"max_uses"`
		Labels     map[string]string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.NameHint = strings.TrimSpace(req.NameHint)
	req.DNS = strings.TrimSpace(req.DNS)
	req.PoolName = strings.TrimSpace(req.PoolName)
	req.TargetRole = strings.TrimSpace(req.TargetRole)
	req.DeviceName = strings.TrimSpace(req.DeviceName)

	callerRole, _ := r.Context().Value(ContextKeyRole).(store.Role)
	if callerRole == store.RoleAdmin && req.TargetRole == "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "admin cannot create owner-level invites",
		})
		return
	}

	var ttl time.Duration
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
	} else {
		ttl = 72 * time.Hour
	}

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
	if req.PoolName != "" {
		opts = append(opts, store.WithPool(req.PoolName))
	}
	if req.TargetRole != "" {
		opts = append(opts, store.WithTargetRole(req.TargetRole))
	}
	if req.DeviceName != "" {
		opts = append(opts, store.WithDeviceName(req.DeviceName))
	}
	if req.MaxUses > 0 {
		opts = append(opts, store.WithMaxUses(req.MaxUses))
	}
	if len(req.Labels) > 0 {
		opts = append(opts, store.WithLabels(req.Labels))
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
		"invite_id":     inv.ID,
		"token":         rawToken,
		"expires_at":    inv.ExpiresAt,
		"bootstrap_url": fmt.Sprintf("%s/bootstrap?token=%s", h.cfg().PublicURL(), rawToken),
		"command":       fmt.Sprintf("curl -sSf \"%s/bootstrap?token=%s&name=DEVICE_NAME\" | sudo bash", h.cfg().PublicURL(), rawToken),
		"message":       "Share this token with the client. It will only be shown once.",
	})
}

func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	_ = h.store.ExpireInvites()

	showDeleted := r.URL.Query().Get("show_deleted") == "true"
	invites := h.store.ListInvites(showDeleted)

	type inviteInfo struct {
		ID              string            `json:"id"`
		Status          string            `json:"status"`
		CreatedAt       string            `json:"created_at"`
		ExpiresAt       string            `json:"expires_at,omitempty"`
		RedeemedAt      string            `json:"redeemed_at,omitempty"`
		RedeemedBy      string            `json:"redeemed_by,omitempty"`
		RevokedAt       string            `json:"revoked_at,omitempty"`
		DeletedAt       string            `json:"deleted_at,omitempty"`
		DeletedBy       string            `json:"deleted_by,omitempty"`
		IssuedBy        string            `json:"issued_by"`
		DisplayNameHint string            `json:"display_name_hint,omitempty"`
		DNSOverride     string            `json:"dns_override,omitempty"`
		PoolName        string            `json:"pool_name,omitempty"`
		TargetRole      string            `json:"target_role,omitempty"`
		DeviceName      string            `json:"device_name,omitempty"`
		MaxUses         int               `json:"max_uses,omitempty"`
		UsedCount       int               `json:"used_count,omitempty"`
		Labels          map[string]string `json:"labels,omitempty"`
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
			DeletedAt:       inv.DeletedAt,
			DeletedBy:       inv.DeletedBy,
			IssuedBy:        inv.IssuedBy,
			DisplayNameHint: inv.DisplayNameHint,
			DNSOverride:     inv.DNSOverride,
			PoolName:        inv.PoolName,
			TargetRole:      inv.TargetRole,
			DeviceName:      inv.DeviceName,
			MaxUses:         inv.MaxUses,
			UsedCount:       inv.UsedCount,
			Labels:          inv.Labels,
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

	operator := "apikey"
	if user, ok := r.Context().Value(ContextKeyUser).(string); ok && user != "apikey" {
		operator = user
	}

	if r.URL.Query().Get("action") == "delete" {
		if err := h.store.DeleteInvite(id, operator); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}

		if err := h.store.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
			return
		}

		audit.Log("invite_deleted", auditFields("invite_id", id, "deleted_by", operator))

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("invite %q deleted", id),
		})
		return
	}

	if r.URL.Query().Get("action") == "force-delete" {
		if err := h.store.ForceDeleteInvite(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}

		if err := h.store.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
			return
		}

		audit.Log("invite_force_deleted", auditFields("invite_id", id, "deleted_by", operator))

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("invite %q permanently force-deleted", id),
		})
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

// InviteLink returns the bootstrap URL and shell command for an existing
// invite without consuming it. Accepts an invite ID or raw token via the
// path segment, and an optional device name via ?name= query parameter.
// Access: LocalOnly + RequireRole(admin, owner).
func (h *Handler) InviteLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Path: /api/v1/invites/{id}/link
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/invites/")
	if !strings.HasSuffix(path, "/link") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite link endpoint not found"})
		return
	}
	id := strings.TrimSpace(strings.TrimSuffix(path, "/link"))
	if decodedID, err := url.PathUnescape(id); err == nil {
		id = decodedID
	}
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite ID or token is required"})
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "my-device"
	}
	if err := validatePeerName(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Try ID first, then token hash lookup.
	inv, ok := h.store.GetInviteByID(id)
	foundByID := ok
	if !ok {
		// Try as raw token hash.
		tokenSum := sha256.Sum256([]byte(id))
		tokenHash := hex.EncodeToString(tokenSum[:])
		inv, ok = h.store.GetInviteByTokenHash(tokenHash)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite not found"})
			return
		}
	}

	// Build bootstrap URL using the server's public URL.
	publicURL := h.cfg().PublicURL()
	escapedName := url.QueryEscape(name)

	if foundByID {
		if inv.RawToken == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"invite_id": inv.ID,
				"status":    string(inv.Status),
				"note":      "This invite was created before raw-token retention, so the full onboarding URL cannot be reconstructed. Use the original token if available, or create a new invite.",
			})
			return
		}

		bootstrapURL := fmt.Sprintf("%s/bootstrap?token=%s&name=%s", publicURL, url.QueryEscape(inv.RawToken), escapedName)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"invite_id":     inv.ID,
			"status":        string(inv.Status),
			"bootstrap_url": bootstrapURL,
			"command":       fmt.Sprintf("curl -sSf \"%s\" | sudo bash", bootstrapURL),
			"inspect":       fmt.Sprintf("curl -sSf \"%s\"", bootstrapURL),
		})
		return
	}

	// Found by raw token — reconstruct the bootstrap URL.
	bootstrapURL := fmt.Sprintf("%s/bootstrap?token=%s&name=%s", publicURL, url.QueryEscape(id), escapedName)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invite_id":     inv.ID,
		"status":        string(inv.Status),
		"bootstrap_url": bootstrapURL,
		"command":       fmt.Sprintf("curl -sSf \"%s\" | sudo bash", bootstrapURL),
		"inspect":       fmt.Sprintf("curl -sSf \"%s\"", bootstrapURL),
	})
}

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
		Alias           string `json:"alias,omitempty"`
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
			Alias:     p.Alias,
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

// PeerAliasUpdate handles PUT /api/v1/peers/alias
// Body: {"pubkey": "<public_key>", "alias": "<new_alias>"}
// Updates the alias of a peer identified by its immutable public key.
func (h *Handler) PeerAliasUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Pubkey string `json:"pubkey"`
		Alias  string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	req.Pubkey = strings.TrimSpace(req.Pubkey)
	req.Alias = strings.TrimSpace(req.Alias)

	if req.Pubkey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pubkey is required"})
		return
	}
	if req.Alias == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alias is required"})
		return
	}
	if err := validatePeerName(req.Alias); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	peer, ok := h.store.PeerByPublicKey(req.Pubkey)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("peer with public key %q not found", req.Pubkey),
		})
		return
	}

	oldAlias := peer.Alias
	if err := h.store.SetPeerAlias(peer.Name, req.Alias); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update alias"})
		return
	}

	if err := h.store.Save(); err != nil {
		// Rollback alias to old value.
		_ = h.store.SetPeerAlias(peer.Name, oldAlias)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("peer_alias_changed", auditFields("name", peer.Name, "old", oldAlias, "new", req.Alias))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"name":      peer.Name,
		"pubkey":    peer.PublicKey,
		"old_alias": oldAlias,
		"new_alias": req.Alias,
	})
}

// PeerDeleteByPubkey handles DELETE /api/v1/peers/by-pubkey/{pubkey}
// Deletes a peer by its immutable public key.
func (h *Handler) PeerDeleteByPubkey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	pubkey := strings.TrimPrefix(r.URL.Path, "/api/v1/peers/by-pubkey/")
	if pubkey == "" || pubkey == r.URL.Path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peer public key is required"})
		return
	}

	peer, ok := h.store.PeerByPublicKey(pubkey)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("peer with public key %q not found", pubkey),
		})
		return
	}

	if err := h.wgMgr.RemovePeerByKey(h.cfg().WGInterface, peer.PublicKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from wireguard"})
		return
	}

	if err := h.store.RemovePeer(peer.Name); err != nil {
		log.Printf("ROLLBACK: re-adding peer %q to WG after store remove failure: %v", peer.Name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from store"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		h.store.AddPeer(peer)
		log.Printf("ROLLBACK: re-adding peer %q to WG after config write failure: %v", peer.Name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		h.store.AddPeer(peer)
		log.Printf("ROLLBACK: re-adding peer %q to WG after state save failure: %v", peer.Name, err)
		h.wgMgr.AddPeerLive(h.cfg().WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

	audit.Log("peer_deleted", auditFields("name", peer.Name, "ip", peer.Address, "admin", remoteIP(r)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("peer %q removed", peer.Name),
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

	publicHost := h.cfg().PublicHost()
	publicURL := h.cfg().PublicURL()

	script := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
# WG-Manager — Invite Bootstrap Script
# Served by /bootstrap — inspect before running: curl -sSf %s/bootstrap
# Usage: curl -sSf "%s/bootstrap?token=INVITE_TOKEN&name=MYDEVICE" | sudo bash

SERVER_URL=%s
SERVER_HOST=%s
INVITE_TOKEN=%s
PEER_NAME=%s
DEFAULT_DNS=%s

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; }

if [ "${INVITE_TOKEN}" = "" ]; then
    err "Missing invite token. Usage: curl -sSf \"$SERVER_URL/bootstrap?token=INVITE_TOKEN&name=MYDEVICE\" | sudo bash"
    exit 1
fi

# ── OS detection ──
detect_os() {
    if grep -qi microsoft /proc/version 2>/dev/null || [ -f /proc/sys/fs/binfmt_misc/WSLInterop ]; then
        echo "wsl"
        return
    fi
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "macos" ;;
        *)       echo "unknown" ;;
    esac
}
OS=$(detect_os)

# ── WSL warning ──
if [ "$OS" = "wsl" ]; then
    warn "WSL detected. WireGuard should be installed on the Windows host, not inside WSL."
    warn "The WireGuard kernel module is not available inside WSL."
    warn "After installing WireGuard on Windows, re-run this script from the host."
    warn "Continuing anyway — config file will be saved but the tunnel may not work."
fi

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

# ── Preflight: determine JSON parser strategy ──
PARSER_MODE=""
PARSER_OK=0
parsers_suggest_install() {
    local reason="${1:-JSON parsing}"
    local pkg=""
    for mgr in apt-get yum dnf apk brew; do
        if command -v "$mgr" &>/dev/null; then pkg="$mgr"; break; fi
    done
    if [ -n "$pkg" ]; then
        echo ""
        warn "Would you like to install jq? (y/N)"
        local answer
        read -r answer </dev/tty 2>/dev/null || true
        if [ "$answer" = "y" ] || [ "$answer" = "Y" ]; then
            case "$pkg" in
                apt-get) sudo apt-get update -qq && sudo apt-get install -y -qq jq ;;
                yum)     sudo yum install -y jq ;;
                dnf)     sudo dnf install -y jq ;;
                apk)     sudo apk add jq ;;
                brew)    brew install jq ;;
            esac
            if command -v jq &>/dev/null; then
                PARSER_MODE="jq"
                PARSER_OK=1
                return 0
            fi
        fi
        err "Cannot proceed: ${reason}. Install jq or python3 and re-run."
        if [ "$pkg" = "apt-get" ]; then
            err "  sudo apt-get install jq"
        elif [ "$pkg" = "brew" ]; then
            err "  brew install jq"
        else
            err "  sudo $pkg install jq"
        fi
    else
        err "Cannot proceed: ${reason}. Install jq or python3 and re-run."
    fi
    exit 1
}

if command -v jq &>/dev/null; then
    PARSER_MODE="jq"
    PARSER_OK=1
elif command -v python3 &>/dev/null; then
    PARSER_MODE="python3"
    PARSER_OK=1
elif command -v grep &>/dev/null && command -v sed &>/dev/null; then
    PARSER_MODE="fallback"
    PARSER_OK=1
    warn "jq and python3 not found — using basic grep/sed JSON parser."
    warn "Install jq (sudo apt install jq) for more robust output."
else
    parsers_suggest_install "no JSON parser (jq, python3, or grep+sed) found"
fi

# ── JSON parsing (jq → python3 → grep/sed fallback) ──
json_get() {
    local json="$1" key="$2" default="${3:-}"
    if [ "$PARSER_MODE" = "jq" ]; then
        echo "$json" | jq -r ".$key" 2>/dev/null || echo "$default"
    elif [ "$PARSER_MODE" = "python3" ]; then
        echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$key','$default'))" 2>/dev/null || echo "$default"
    else
        # grep/sed fallback: match known top-level keys
        case "$key" in
            success)
                if echo "$json" | grep -qE '"success"[[:space:]]*:[[:space:]]*true'; then
                    echo "true"
                else
                    echo "${default:-false}"
                fi
                ;;
            error)
                echo "$json" | sed -n 's/.*"error"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
                ;;
            *)
                # Try string value
                local val
                val=$(echo "$json" | grep -oE "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | cut -d'"' -f4)
                [ -n "$val" ] && { echo "$val"; return; }
                # Try numeric value
                val=$(echo "$json" | grep -oE "\"${key}\"[[:space:]]*:[[:space:]]*[0-9]+" | head -1 | grep -oE '[0-9]+$')
                [ -n "$val" ] && { echo "$val"; return; }
                # Try boolean
                if echo "$json" | grep -qE "\"${key}\"[[:space:]]*:[[:space:]]*true"; then echo "true"; return; fi
                if echo "$json" | grep -qE "\"${key}\"[[:space:]]*:[[:space:]]*false"; then echo "false"; return; fi
                echo "$default"
                ;;
        esac
    fi
}

json_get_nested() {
    local json="$1" key1="$2" key2="$3" default="${4:-}"
    if [ "$PARSER_MODE" = "jq" ]; then
        echo "$json" | jq -r ".${key1}.${key2}" 2>/dev/null || echo "$default"
    elif [ "$PARSER_MODE" = "python3" ]; then
        echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$key1',{}).get('$key2','$default'))" 2>/dev/null || echo "$default"
    else
        # grep/sed fallback: extract string or numeric value for key2
        local val
        val=$(echo "$json" | grep -oE "\"${key2}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | cut -d'"' -f4)
        [ -n "$val" ] && { echo "$val"; return; }
        val=$(echo "$json" | grep -oE "\"${key2}\"[[:space:]]*:[[:space:]]*[0-9]+" | head -1 | grep -oE '[0-9]+$')
        [ -n "$val" ] && { echo "$val"; return; }
        # Try boolean
        if echo "$json" | grep -qE "\"${key2}\"[[:space:]]*:[[:space:]]*true"; then echo "true"; return; fi
        if echo "$json" | grep -qE "\"${key2}\"[[:space:]]*:[[:space:]]*false"; then echo "false"; return; fi
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
HTTP_CODE=$(curl -sS -w "%%{http_code}" -o /tmp/wg-bootstrap-resp.json \
    -X POST "$SERVER_URL/api/v1/redeem" \
    -H "Content-Type: application/json" \
    -d "{\"token\":\"$INVITE_TOKEN\",\"name\":\"$PEER_NAME\",\"dns\":\"$DEFAULT_DNS\"}")
RESP=$(cat /tmp/wg-bootstrap-resp.json 2>/dev/null || echo "{}")
rm -f /tmp/wg-bootstrap-resp.json

if [ "$HTTP_CODE" != "200" ]; then
    ERR_MSG=$(json_get "$RESP" "error" "unknown error")
    err "Failed to redeem invite (HTTP $HTTP_CODE): $ERR_MSG"
    exit 1
fi

# HTTP 200 — server accepted, token IS consumed.
SUCCESS=$(json_get "$RESP" "success" "")
if [ "$SUCCESS" != "true" ]; then
    if [ "$PARSER_MODE" = "fallback" ]; then
        err "Redeem succeeded (HTTP 200) but the grep/sed fallback could not parse the response."
    else
        err "Redeem succeeded (HTTP 200) but JSON parsing failed."
    fi
    err ""
    err "The invite token WAS CONSUMED and is now a one-time used token."
    err "It cannot be reused — you need a NEW invite from your admin."
    err ""
    err "Recovery options:"
    err "  1. Install jq or python3 and re-run with a fresh token"
    err "  2. Contact your administrator to re-issue an invite"
    err "  3. Share the raw response below with your admin for manual config recovery"
    err ""
    if [ "$PARSER_MODE" = "fallback" ]; then
        pkg=""
        for mgr in apt-get yum dnf apk brew; do
            if command -v "$mgr" &>/dev/null; then pkg="$mgr"; break; fi
        done
        if [ -n "$pkg" ]; then
            echo ""
            warn "Would you like to install jq now? (y/N)"
            answer=""
            read -r answer </dev/tty 2>/dev/null || true
            if [ "$answer" = "y" ] || [ "$answer" = "Y" ]; then
                case "$pkg" in
                    apt-get) sudo apt-get update -qq && sudo apt-get install -y -qq jq ;;
                    yum)     sudo yum install -y jq ;;
                    dnf)     sudo dnf install -y jq ;;
                    apk)     sudo apk add jq ;;
                    brew)    brew install jq ;;
                esac
                if command -v jq &>/dev/null; then
                    log "jq installed. Re-running parse..."
                    SUCCESS=$(echo "$RESP" | jq -r '.success' 2>/dev/null || echo "")
                fi
            fi
        fi
        if [ "$SUCCESS" != "true" ]; then
            err "Raw response from server:"
            echo "$RESP"
            exit 1
        fi
    else
        err "Raw response from server:"
        echo "$RESP"
        exit 1
    fi
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
`, publicURL, publicURL,
		shellQuote(publicURL), shellQuote(publicHost), shellQuote(token), shellQuote(name),
		shellQuote(h.cfg().DefaultDNS), h.cfg().WGServerIP)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
	if err != nil {
		return "58880"
	}
	if n, _ := strconv.Atoi(p); n > 0 {
		return p
	}
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
