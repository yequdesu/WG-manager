package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wire-guard-dev/internal/audit"
	"wire-guard-dev/internal/store"
	"wire-guard-dev/internal/wg"
)

type Config struct {
	WGInterface          string
	WGPort               int
	WGSubnet             string
	WGServerIP           string
	MgmtListen           string
	APIKey               string
	ServerPublicIP       string
	DefaultDNS           string
	PeerKeepalive        int
	PeersDBPath          string
	WGConfPath           string
}

func (c *Config) ServerEndpoint() string {
	return fmt.Sprintf("%s:%d", c.ServerPublicIP, c.WGPort)
}

type Handler struct {
	store  *store.State
	wgMgr  *wg.Manager
	config *Config
}

func NewHandler(s *store.State, m *wg.Manager, cfg *Config) *Handler {
	return &Handler{
		store:  s,
		wgMgr:  m,
		config: cfg,
	}
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

	if h.store.HasPeer(req.Hostname) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("peer %q already exists", req.Hostname),
			"hint":  "contact admin to remove the existing peer first",
		})
		return
	}

	dns := req.DNS
	if dns == "" {
		dns = h.config.DefaultDNS
	}

	privateKey, publicKey, err := h.wgMgr.GenKeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
		return
	}

	ip, err := h.store.NextAvailableIP(h.config.WGSubnet)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no available IP addresses"})
		return
	}

	peer := store.Peer{
		Name:       req.Hostname,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Address:    ip,
		DNS:        dns,
		Keepalive:  h.config.PeerKeepalive,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if err := h.store.AddPeer(peer); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save peer"})
		return
	}

	allowedIP := fmt.Sprintf("%s/32", peer.Address)
		if err := h.wgMgr.AddPeerLive(h.config.WGInterface, peer.PublicKey, allowedIP, peer.Keepalive); err != nil {
			h.store.RemovePeer(peer.Name)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add peer to wireguard"})
			return
		}

	if err := h.writeConfigToDisk(); err != nil {
		h.wgMgr.RemovePeerByKey(h.config.WGInterface, peer.PublicKey)
		h.store.RemovePeer(peer.Name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		h.wgMgr.RemovePeerByKey(h.config.WGInterface, peer.PublicKey)
		h.store.RemovePeer(peer.Name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
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
			"server_endpoint":   h.config.ServerEndpoint(),
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
	script = strings.ReplaceAll(script, "__SERVER_PUBLIC_IP__", h.config.ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.config.MgmtListen))
	script = strings.ReplaceAll(script, "__API_KEY__", h.config.APIKey)
	script = strings.ReplaceAll(script, "__DEFAULT_DNS__", h.config.DefaultDNS)
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.config.WGSubnet)
	if name != "" {
		script = strings.ReplaceAll(script, "__PEER_NAME__", name)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) serveApprovalBash(w http.ResponseWriter, r *http.Request, name string) {
	script := embedApprovalSh
	script = strings.ReplaceAll(script, "__SERVER_IP__", h.config.ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.config.MgmtListen))
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.config.WGSubnet)
	if name != "" {
		script = strings.ReplaceAll(script, "__PEER_NAME__", name)
	}
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
	dns := strings.TrimSpace(q.Get("dns"))
	if dns == "" {
		dns = h.config.DefaultDNS
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
		ip, err = h.store.NextAvailableIP(h.config.WGSubnet)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no available IP addresses"})
			return
		}
		peer := store.Peer{
			Name: name, PublicKey: publicKey, PrivateKey: privateKey,
			Address: ip, DNS: dns, Keepalive: h.config.PeerKeepalive,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := h.store.AddPeer(peer); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save peer"})
			return
		}
		allowedIP := fmt.Sprintf("%s/32", ip)
		if err := h.wgMgr.AddPeerLive(h.config.WGInterface, publicKey, allowedIP, h.config.PeerKeepalive); err != nil {
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add peer"})
				return
			}
			if err := h.writeConfigToDisk(); err != nil {
				h.wgMgr.RemovePeerByKey(h.config.WGInterface, publicKey)
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
				return
			}
			if err := h.store.Save(); err != nil {
				h.wgMgr.RemovePeerByKey(h.config.WGInterface, publicKey)
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
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
`, ip, privateKey, dns, h.store.Server().PublicKey, h.config.ServerEndpoint(), h.config.WGSubnet, h.config.PeerKeepalive)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.conf", name))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(conf))
}

func (h *Handler) serveApprovalPS1(w http.ResponseWriter, r *http.Request) {
	script := embedApprovalPs1
	script = strings.ReplaceAll(script, "__SERVER_IP__", h.config.ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", portStr(h.config.MgmtListen))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request) {
	ip := h.config.ServerPublicIP
	html := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>WG-Manager Connect</title>
<style>body{font:14px/1.5 monospace;max-width:680px;margin:40px auto;background:#111;color:#eee}
h1{color:#5af} .box{background:#222;padding:12px;margin:8px 0;border-radius:4px;border:1px solid #444}
pre{margin:0;white-space:pre-wrap;word-break:break-all;color:#afa} .cmd{color:#ff8}
label{display:inline-block;min-width:80px}.tab{display:inline-block;padding:4px 12px;cursor:pointer;border:1px solid #555;border-bottom:none;margin-right:4px;border-radius:4px 4px 0 0}.tab.active{background:#333;color:#5af;font-weight:bold}
.platform{display:none}.platform.active{display:block}a{color:#5af}
</style></head><body>
<h1>WG-Manager Connect</h1>
<p>Server: ` + ip + `</p>
<div><span class="tab active" onclick="show('linux')">Linux / macOS / WSL</span><span class="tab" onclick="show('windows')">Windows</span><span class="tab" onclick="show('browser')">Browser</span></div>
<div id="linux" class="platform active"><div class="box"><p>Approval (default):</p><pre>curl -sSf http://` + ip + `:` + portStr(h.config.MgmtListen) + `/connect | sudo bash</pre></div><div class="box"><p>Direct (admin link):</p><pre>curl -sSf "http://` + ip + `:` + portStr(h.config.MgmtListen) + `/connect?mode=direct&name=my-device" | sudo bash</pre></div></div>
<div id="windows" class="platform"><div class="box"><p>Approval — PowerShell:</p><pre>Invoke-WebRequest http://` + ip + `:` + portStr(h.config.MgmtListen) + `/connect -OutFile t.ps1; .\t.ps1</pre></div><div class="box"><p>Direct — CMD:</p><pre>curl -o wg0.conf "http://` + ip + `:` + portStr(h.config.MgmtListen) + `/connect?mode=direct&name=my-pc"</pre></div></div>
<div id="browser" class="platform"><p>You are already here. Choose a platform tab above to see commands.</p></div>
<script>function show(id){document.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'));document.querySelectorAll('.platform').forEach(p=>p.classList.remove('active'));document.getElementById(id).classList.add('active');event.target.classList.add('active')}</script>
</body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func (h *Handler) serveQR(w http.ResponseWriter, r *http.Request, mode, name string) {
	isDirect := mode == "direct"
	var content string

	if isDirect {
		if name == "" { name = "mobile" }
		q := r.URL.Query()
		dns := q.Get("dns")
		if dns == "" { dns = h.config.DefaultDNS }

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
			ip, err = h.store.NextAvailableIP(h.config.WGSubnet)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no IP available"})
				return
			}
			peer := store.Peer{
				Name: name, PublicKey: publicKey, PrivateKey: privateKey,
				Address: ip, DNS: dns, Keepalive: h.config.PeerKeepalive,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := h.store.AddPeer(peer); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
				return
			}
			allowedIP := fmt.Sprintf("%s/32", ip)
			if err := h.wgMgr.AddPeerLive(h.config.WGInterface, publicKey, allowedIP, h.config.PeerKeepalive); err != nil {
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "wg add failed"})
				return
			}
			if err := h.writeConfigToDisk(); err != nil {
				h.wgMgr.RemovePeerByKey(h.config.WGInterface, publicKey)
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
				return
			}
			if err := h.store.Save(); err != nil {
				h.wgMgr.RemovePeerByKey(h.config.WGInterface, publicKey)
				h.store.RemovePeer(name)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
				return
			}
			audit.Log("peer_registered", auditFields("name", name, "ip", ip, "source", "qr"))
		}
		content = fmt.Sprintf(`[Interface]
Address = %s/24
PrivateKey = %s
DNS = %s

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s
PersistentKeepalive = %d
`, ip, privateKey, dns, h.store.Server().PublicKey, h.config.ServerEndpoint(), h.config.WGSubnet, h.config.PeerKeepalive)
	} else {
		content = fmt.Sprintf("http://%s:%s/connect", h.config.ServerPublicIP, portStr(h.config.MgmtListen))
	}

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
		dns = h.config.DefaultDNS
	}

	privateKey, publicKey, err := h.wgMgr.GenKeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate keys"})
		return
	}

	ip, err := h.store.NextAvailableIP(h.config.WGSubnet)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no available IP addresses"})
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
		Address:    ip,
		Keepalive:  h.config.PeerKeepalive,
		SourceIP:   sourceIP,
	}

	if err := h.store.AddRequest(pendingReq); err != nil {
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
				"server_endpoint":   h.config.ServerEndpoint(),
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

	allowedIP := fmt.Sprintf("%s/32", peer.Address)
	if err := h.wgMgr.AddPeerLive(h.config.WGInterface, peer.PublicKey, allowedIP, peer.Keepalive); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add peer to wireguard"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		h.wgMgr.RemovePeerByKey(h.config.WGInterface, peer.PublicKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		h.wgMgr.RemovePeerByKey(h.config.WGInterface, peer.PublicKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
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
			"server_endpoint": h.config.ServerEndpoint(),
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

	wgStatus, err := h.wgMgr.Show(h.config.WGInterface)
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
		DNS             string `json:"dns"`
		Keepalive       int    `json:"keepalive"`
		CreatedAt       string `json:"created_at"`
		Endpoint        string `json:"endpoint,omitempty"`
		LatestHandshake string `json:"latest_handshake,omitempty"`
		TransferRx      string `json:"transfer_rx,omitempty"`
		TransferTx      string `json:"transfer_tx,omitempty"`
		Online          bool   `json:"online"`
	}

	result := make([]peerInfo, 0, len(peers))
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
		result = append(result, pi)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server_endpoint": h.config.ServerEndpoint(),
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

	if err := h.wgMgr.RemovePeerByKey(h.config.WGInterface, peer.PublicKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from wireguard"})
		return
	}

	if err := h.store.RemovePeer(name); err != nil {
		h.wgMgr.AddPeerLive(h.config.WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from store"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		h.store.AddPeer(peer)
		h.wgMgr.AddPeerLive(h.config.WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		h.store.AddPeer(peer)
		h.wgMgr.AddPeerLive(h.config.WGInterface, peer.PublicKey, fmt.Sprintf("%s/32", peer.Address), peer.Keepalive)
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

	wgStatus, err := h.wgMgr.Show(h.config.WGInterface)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"interface":  h.config.WGInterface,
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
		"interface":   h.config.WGInterface,
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
		h.config.WGConfPath,
		h.config.WGInterface,
		h.config.WGPort,
		h.config.PeerKeepalive,
		h.config.WGServerIP,
		h.store.Server().PrivateKey,
		peerMap,
	)
}

func portStr(addr string) string {
	_, p, err := net.SplitHostPort(addr)
	if err != nil { return "58880" }
	if n, _ := strconv.Atoi(p); n > 0 { return p }
	return "58880"
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	result := bytes.TrimRight(buf.Bytes(), "\n")
	w.Write(result)
}
