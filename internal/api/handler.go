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
	ClientScriptTemplate string
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add peer to wireguard"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

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

func (h *Handler) WindowsConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query parameter is required"})
		return
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
			Name:       name,
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

		allowedIP := fmt.Sprintf("%s/32", ip)
		if err := h.wgMgr.AddPeerLive(h.config.WGInterface, publicKey, allowedIP, h.config.PeerKeepalive); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add peer to wireguard"})
			return
		}

		if err := h.writeConfigToDisk(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
			return
		}

		if err := h.store.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
			return
		}
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove peer from store"})
		return
	}

	if err := h.writeConfigToDisk(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write config"})
		return
	}

	if err := h.store.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist state"})
		return
	}

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

func (h *Handler) ClientScript(w http.ResponseWriter, r *http.Request) {
	script, err := ReadClientScript(h.config.ClientScriptTemplate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load client script template"})
		return
	}

	_, portStr, _ := net.SplitHostPort(h.config.MgmtListen)
	mgmtPort, _ := strconv.Atoi(portStr)
	if mgmtPort == 0 {
		mgmtPort = 58880
	}

	script = strings.ReplaceAll(script, "__SERVER_PUBLIC_IP__", h.config.ServerPublicIP)
	script = strings.ReplaceAll(script, "__MGMT_PORT__", strconv.Itoa(mgmtPort))
	script = strings.ReplaceAll(script, "__API_KEY__", h.config.APIKey)
	script = strings.ReplaceAll(script, "__DEFAULT_DNS__", h.config.DefaultDNS)
	script = strings.ReplaceAll(script, "__WG_ALLOWED_IPS__", h.config.WGSubnet)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=connect.sh")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
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
