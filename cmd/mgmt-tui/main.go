package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	APIURL   string
	APIKey   string
	AuditLog string
}

type Peer struct {
	Name            string `json:"name"`
	Address         string `json:"address"`
	DNS             string `json:"dns"`
	PublicKey       string `json:"public_key"`
	Keepalive       int    `json:"keepalive"`
	CreatedAt       string `json:"created_at"`
	Endpoint        string `json:"endpoint"`
	LatestHandshake string `json:"latest_handshake"`
	TransferRx      string `json:"transfer_rx"`
	TransferTx      string `json:"transfer_tx"`
	Online          bool   `json:"online"`
}

type Request struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	Address   string `json:"address"`
	DNS       string `json:"dns"`
	SourceIP  string `json:"source_ip"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

type PeerListResp struct {
	Peers   []Peer `json:"peers"`
	Count   int    `json:"peer_count"`
	Endpoint string `json:"server_endpoint"`
}

type RequestListResp struct {
	Requests []Request `json:"requests"`
	Count    int       `json:"pending_count"`
}

type StatusResp struct {
	Daemon    string `json:"daemon"`
	Wireguard string `json:"wireguard"`
	Online    int    `json:"peer_online"`
	Total     int    `json:"peer_total"`
	Interface string `json:"interface"`
	Port      string `json:"listen_port"`
}

type APIError struct {
	Error string `json:"error"`
}

var (
	cfg    Config
	peers  PeerListResp
	reqs   RequestListResp
	status StatusResp
	logs   []string

	tab     = 0
	sel     = 0
	detailI = 0
	running = true
	msg     = ""

	w, h = 80, 24
)

func main() {
	loadConfig()
	enableRawMode()
	defer disableRawMode()

	go keyboardLoop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	refresh()
	render()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for running {
		select {
		case <-ticker.C:
			refresh()
			render()
		case <-sig:
			running = false
		}
	}
	disableRawMode()
	fmt.Print("\033[2J\033[H")
	fmt.Println("Goodbye.")
}

func loadConfig() {
	paths := []string{"config.env", os.Getenv("HOME") + "/WG-manager/config.env"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "MGMT_LISTEN=") {
				v := strings.TrimPrefix(line, "MGMT_LISTEN=")
				if v == "0.0.0.0" || strings.HasPrefix(v, "0.0.0.0:") {
					v = strings.Replace(v, "0.0.0.0", "127.0.0.1", 1)
				}
				cfg.APIURL = "http://" + v
			}
			if strings.HasPrefix(line, "MGMT_API_KEY=") {
				cfg.APIKey = strings.TrimPrefix(line, "MGMT_API_KEY=")
			}
			if strings.HasPrefix(line, "AUDIT_LOG_PATH=") {
				cfg.AuditLog = strings.TrimPrefix(line, "AUDIT_LOG_PATH=")
			}
		}
		break
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "http://127.0.0.1:58880"
	}
}

func api(path string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", cfg.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiPost(path string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", cfg.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiDelete(path string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("DELETE", cfg.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func refresh() {
	body, err := api("/api/v1/peers")
	if err == nil {
		json.Unmarshal(body, &peers)
	}
	body, err = api("/api/v1/requests")
	if err == nil {
		json.Unmarshal(body, &reqs)
	}
	body, err = api("/api/v1/status")
	if err == nil {
		json.Unmarshal(body, &status)
	}
	if cfg.AuditLog != "" {
		data, err := os.ReadFile(cfg.AuditLog)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 50 {
				lines = lines[len(lines)-50:]
			}
			logs = lines
		}
	}
	getSize()
}

func getSize() {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err == nil {
		fmt.Sscanf(string(out), "%d %d", &h, &w)
	}
	if h < 10 {
		h = 24
	}
	if w < 40 {
		w = 80
	}
}

func enableRawMode() {
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = os.Stdin
	cmd.Run()
	fmt.Print("\033[?25l") // hide cursor
	fmt.Print("\033[2J")   // clear screen
}

func disableRawMode() {
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	cmd.Run()
	fmt.Print("\033[?25h") // show cursor
}

func keyboardLoop() {
	buf := make([]byte, 6)
	for running {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			continue
		}
		handleKey(buf[:n])
	}
}

func handleKey(b []byte) {
	switch {
	case b[0] == 'q' || b[0] == 3: // Ctrl+C
		running = false
	case b[0] == '\t':
		tab = (tab + 1) % 4
		sel = 0
	case b[0] == 'r':
		msg = "Refreshed"
		refresh()
	case len(b) == 3 && b[0] == 27 && b[1] == 91:
		switch b[2] {
		case 'A': // up
			sel--
			if sel < 0 {
				sel = 0
			}
		case 'B': // down
			sel++
		case 'C': // right (detail switch in peers tab)
			if tab == 0 && len(peers.Peers) > 0 {
				detailI = (detailI + 1) % (len(peers.Peers) + 1)
			}
		case 'D': // left
			if tab == 0 && detailI > 0 {
				detailI--
			}
		}
	case b[0] == 'd' || b[0] == 'D':
		if tab == 0 && sel < len(peers.Peers) {
			deletePeer(peers.Peers[sel].Name)
		} else if tab == 1 && sel < len(reqs.Requests) {
			denyRequest(reqs.Requests[sel].ID)
		}
	case b[0] == 'a' || b[0] == 'A':
		if tab == 1 && sel < len(reqs.Requests) {
			approveRequest(reqs.Requests[sel].ID)
		}
	case b[0] == 'j' && tab == 3:
		sel++
	case b[0] == 'k' && tab == 3:
		sel--
		if sel < 0 {
			sel = 0
		}
	}
	render()
}

func deletePeer(name string) {
	body, err := apiDelete("/api/v1/peers/" + name)
	if err != nil {
		msg = fmt.Sprintf("Delete failed: %v", err)
		return
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &resp)
	if resp.Success {
		msg = fmt.Sprintf("Deleted: %s", name)
	} else {
		msg = fmt.Sprintf("Error: %s", resp.Error)
	}
	refresh()
}

func approveRequest(id string) {
	body, err := apiPost("/api/v1/requests/" + id + "/approve")
	if err != nil {
		msg = fmt.Sprintf("Approve failed: %v", err)
		return
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &resp)
	if resp.Success {
		msg = "Approved!"
	} else {
		msg = fmt.Sprintf("Error: %s", resp.Error)
	}
	refresh()
}

func denyRequest(id string) {
	body, err := apiDelete("/api/v1/requests/" + id)
	if err != nil {
		msg = fmt.Sprintf("Deny failed: %v", err)
		return
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &resp)
	if resp.Success {
		msg = "Denied"
	} else {
		msg = fmt.Sprintf("Error: %s", resp.Error)
	}
	refresh()
}

func render() {
	var buf strings.Builder

	// ── Header ──
	buf.WriteString("\033[H\033[K")
	buf.WriteString(fmt.Sprintf("\033[1;37;44m WG-Manager TUI \033[0m "))
	buf.WriteString(fmt.Sprintf("\033[36m%d peers\033[0m  ", len(peers.Peers)))
	buf.WriteString(fmt.Sprintf("\033[33m%d pending\033[0m  ", len(reqs.Requests)))
	buf.WriteString(fmt.Sprintf("\033[32monline: %d/%d\033[0m", status.Online, len(peers.Peers)))
	buf.WriteString("\033[K\n")

	// ── Tab bar ──
	tabs := []string{" Peers ", " Requests ", " Status ", " Log "}
	for i, t := range tabs {
		if i == tab {
			buf.WriteString(fmt.Sprintf("\033[7m%s\033[0m", t))
		} else {
			buf.WriteString(fmt.Sprintf("\033[37m%s\033[0m", t))
		}
	}
	buf.WriteString("\033[K\n")

	// ── Content ──
	contentH := h - 4
	if contentH < 6 {
		contentH = 6
	}

	switch tab {
	case 0:
		renderPeers(&buf, contentH)
	case 1:
		renderRequests(&buf, contentH)
	case 2:
		renderStatus(&buf, contentH)
	case 3:
		renderLog(&buf, contentH)
	}

	// ── Bottom bar ──
	buf.WriteString(fmt.Sprintf("\033[%d;1H\033[2K", h))
	switch tab {
	case 0:
		buf.WriteString("\033[37m ↑↓:Select  d:Delete  r:Refresh  Tab:Switch  q:Quit\033[0m")
	case 1:
		buf.WriteString("\033[37m ↑↓:Select  a:Approve  d:Deny  r:Refresh  Tab:Switch  q:Quit\033[0m")
	case 2:
		buf.WriteString("\033[37m r:Refresh  Tab:Switch  q:Quit\033[0m")
	case 3:
		buf.WriteString("\033[37m j/k:Scroll  Tab:Switch  q:Quit\033[0m")
	}

	// ── Message flash ──
	if msg != "" {
		buf.WriteString(fmt.Sprintf("\033[%d;1H\033[K\033[33m%s\033[0m", h-1, msg))
		msg = ""
	}

	fmt.Print(buf.String())
}

func renderPeers(buf *strings.Builder, maxLines int) {
	buf.WriteString(fmt.Sprintf("\033[2K%-4s %-20s %-14s %-6s %-8s\n", "#", "Name", "IP", "Online", "Handshake"))
	line := 0
	for i, p := range peers.Peers {
		if line >= maxLines-1 {
			break
		}
		prefix := "  "
		if i == sel {
			prefix = "\033[7m  \033[0m"
		}
		online := " "
		if p.Online {
			online = "\033[32m✓\033[0m"
		}
		hs := p.LatestHandshake
		if hs == "0" {
			hs = "-"
		} else {
			if s, err := strconv.Atoi(hs); err == nil {
				hs = fmt.Sprintf("%ds", s)
			}
		}
		endpoint := p.Endpoint
		if endpoint == "(none)" {
			endpoint = ""
		}
		buf.WriteString(fmt.Sprintf("\033[K%s%-4d %-20s %-14s %-6s %-8s %s\n",
			prefix, i+1, trunc(p.Name, 20), p.Address, online, hs, endpoint))
		line++
	}
	for ; line < maxLines; line++ {
		buf.WriteString("\033[K\n")
	}
}

func renderRequests(buf *strings.Builder, maxLines int) {
	buf.WriteString(fmt.Sprintf("\033[2K%-4s %-20s %-14s %-12s %-10s\n", "#", "Hostname", "IP", "Source", "Age"))
	line := 0
	for i, r := range reqs.Requests {
		if line >= maxLines-1 {
			break
		}
		prefix := "  "
		if i == sel {
			prefix = "\033[7m  \033[0m"
		}
		age := "?"
		if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
			d := time.Since(t)
			if d < time.Minute {
				age = fmt.Sprintf("%ds", int(d.Seconds()))
			} else if d < time.Hour {
				age = fmt.Sprintf("%dm", int(d.Minutes()))
			} else {
				age = fmt.Sprintf("%dh", int(d.Hours()))
			}
		}
		buf.WriteString(fmt.Sprintf("\033[K%s%-4d %-20s %-14s %-12s %-10s\n",
			prefix, i+1, trunc(r.Hostname, 20), r.Address, trunc(r.SourceIP, 12), age))
		line++
	}
	if len(reqs.Requests) == 0 {
		buf.WriteString("\033[K  (no pending requests)\n")
		line++
	}
	for ; line < maxLines; line++ {
		buf.WriteString("\033[K\n")
	}
}

func renderStatus(buf *strings.Builder, maxLines int) {
	buf.WriteString(fmt.Sprintf("\033[K  Daemon:      %s\n", colorStatus(status.Daemon)))
	buf.WriteString(fmt.Sprintf("\033[K  WireGuard:   %s\n", colorStatus(status.Wireguard)))
	buf.WriteString(fmt.Sprintf("\033[K  Interface:   %s (port %s)\n", status.Interface, status.Port))
	buf.WriteString(fmt.Sprintf("\033[K  Peers online: %d / %d\n", status.Online, status.Total))
	buf.WriteString("\033[K\n")
	buf.WriteString("\033[K  Peers:\n")
	for i, p := range peers.Peers {
		if i >= maxLines-7 {
			break
		}
		online := "\033[31m●\033[0m"
		if p.Online {
			online = "\033[32m●\033[0m"
		}
		rx, tx := "0", "0"
		if p.TransferRx != "0" || p.TransferTx != "0" {
			rx = formatBytes(p.TransferRx)
			tx = formatBytes(p.TransferTx)
		}
		buf.WriteString(fmt.Sprintf("\033[K    %s %-20s %s  rx:%s tx:%s\n",
			online, trunc(p.Name, 20), p.Address, rx, tx))
	}
	for i := len(peers.Peers); i < maxLines-7; i++ {
		buf.WriteString("\033[K\n")
	}
}

func renderLog(buf *strings.Builder, maxLines int) {
	start := len(logs) - maxLines
	if start < 0 {
		start = 0
	}
	for i := start; i < len(logs); i++ {
		line := logs[i]
		color := "\033[37m"
		if strings.Contains(line, "approved") {
			color = "\033[32m"
		} else if strings.Contains(line, "rejected") || strings.Contains(line, "deleted") {
			color = "\033[31m"
		} else if strings.Contains(line, "submitted") {
			color = "\033[33m"
		}
		buf.WriteString(fmt.Sprintf("\033[K%s%s\033[0m\n", color, trunc(line, w-2)))
	}
	for i := 0; i < maxLines-len(logs); i++ {
		buf.WriteString("\033[K\n")
	}
}

func colorStatus(s string) string {
	if s == "running" || s == "ok" {
		return "\033[32m" + s + "\033[0m"
	}
	return "\033[31m" + s + "\033[0m"
}

func formatBytes(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func trunc(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
