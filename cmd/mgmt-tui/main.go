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
	Peers    []Peer `json:"peers"`
	Count    int    `json:"peer_count"`
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

var (
	cfg    Config
	peers  PeerListResp
	reqs   RequestListResp
	status StatusResp
	logs   []string

	tab       = 0
	sel       = 0
	running   = true
	msg       = ""
	needRedraw = true

	termW, termH = 80, 24
)

func main() {
	loadConfig()
	enterAltScreen()
	defer exitAltScreen()
	enableRawMode()
	defer disableRawMode()

	getTermSize()
	refresh()

	go keyboardLoop()
	go autoRefresh()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for running {
		if needRedraw {
			render()
		}
		time.Sleep(50 * time.Millisecond)
	}

	exitAltScreen()
	disableRawMode()
	fmt.Print("\033[?1049l\033[?25h\033[H\033[J")
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
				v = strings.Replace(v, "0.0.0.0", "127.0.0.1", 1)
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

func apiGet(path string) ([]byte, error) {
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
	body, err := apiGet("/api/v1/peers")
	if err == nil {
		json.Unmarshal(body, &peers)
	}
	body, err = apiGet("/api/v1/requests")
	if err == nil {
		json.Unmarshal(body, &reqs)
	}
	body, err = apiGet("/api/v1/status")
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
	getTermSize()
}

func getTermSize() {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err == nil {
		fmt.Sscanf(string(out), "%d %d", &termH, &termW)
	}
	if termH < 10 {
		termH = 24
	}
	if termW < 40 {
		termW = 80
	}
}

func enterAltScreen() {
	fmt.Print("\033[?1049h\033[H\033[J")
}

func exitAltScreen() {
	fmt.Print("\033[?1049l")
}

func enableRawMode() {
	exec.Command("stty", "raw", "-echo").Run()
	fmt.Print("\033[?25l")
}

func disableRawMode() {
	exec.Command("stty", "sane").Run()
	fmt.Print("\033[?25h")
}

func keyboardLoop() {
	buf := make([]byte, 6)
	for running {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		b := buf[:n]
		switch {
		case b[0] == 'q' || b[0] == 3:
			running = false
		case b[0] == '\t':
			tab = (tab + 1) % 4
			sel = 0
			needRedraw = true
		case b[0] == 'r':
			msg = "Refreshed"
			refresh()
			needRedraw = true
		case len(b) >= 3 && b[0] == 27 && b[1] == 91:
			switch b[2] {
			case 'A':
				sel--
				if sel < 0 {
					sel = 0
				}
				needRedraw = true
			case 'B':
				sel++
				needRedraw = true
			case 'C':
				if tab == 0 && sel >= len(peers.Peers) {
					sel = 0
				}
				needRedraw = true
			case 'D':
				needRedraw = true
			}
		case b[0] == 'd' || b[0] == 'D':
			switch {
			case tab == 0 && sel < len(peers.Peers):
				deletePeer(peers.Peers[sel].Name)
			case tab == 1 && sel < len(reqs.Requests):
				denyRequest(reqs.Requests[sel].ID)
			}
		case b[0] == 'a' || b[0] == 'A':
			if tab == 1 && sel < len(reqs.Requests) {
				approveRequest(reqs.Requests[sel].ID)
			}
		case b[0] == 'j' && tab == 3:
			sel++
			needRedraw = true
		case b[0] == 'k' && tab == 3:
			sel--
			if sel < 0 {
				sel = 0
			}
			needRedraw = true
		}
	}
}

func autoRefresh() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for running {
		<-ticker.C
		refresh()
		needRedraw = true
	}
}

func deletePeer(name string) {
	body, err := apiDelete("/api/v1/peers/" + name)
	if err != nil {
		msg = fmt.Sprintf("Delete failed: %v", err)
		needRedraw = true
		return
	}
	var r struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = fmt.Sprintf("Deleted: %s", name)
	} else {
		msg = fmt.Sprintf("Error: %s", r.Error)
	}
	refresh()
	needRedraw = true
}

func approveRequest(id string) {
	body, err := apiPost("/api/v1/requests/" + id + "/approve")
	if err != nil {
		msg = fmt.Sprintf("Approve failed: %v", err)
		needRedraw = true
		return
	}
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Approved!"
	} else {
		msg = fmt.Sprintf("Error: %s", r.Error)
	}
	refresh()
	needRedraw = true
}

func denyRequest(id string) {
	body, err := apiDelete("/api/v1/requests/" + id)
	if err != nil {
		msg = fmt.Sprintf("Deny failed: %v", err)
		needRedraw = true
		return
	}
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Denied"
	} else {
		msg = fmt.Sprintf("Error: %s", r.Error)
	}
	refresh()
	needRedraw = true
}

func render() {
	var b strings.Builder
	b.Grow(termW * termH * 2)

	// Row 0: header
	b.WriteString(fmt.Sprintf("\033[1;1H\033[7m WG-Manager TUI \033[0m %d peers  %d pending  online: %d/%d",
		len(peers.Peers), len(reqs.Requests), status.Online, len(peers.Peers)))
	b.WriteString(clearToEOL())

	// Row 1: tab bar
	b.WriteString(fmt.Sprintf("\033[2;1H"))
	tabs := []string{" Peers ", " Requests ", " Status ", " Log "}
	for i, t := range tabs {
		if i == tab {
			b.WriteString(fmt.Sprintf("\033[7m%s\033[0m", t))
		} else {
			b.WriteString(t)
		}
	}
	b.WriteString(clearToEOL())

	// Rows 2..h-2: content
	contentH := termH - 3
	if contentH < 4 {
		contentH = 4
	}

	switch tab {
	case 0:
		renderPeers(&b, contentH)
	case 1:
		renderRequests(&b, contentH)
	case 2:
		renderStatus(&b, contentH)
	case 3:
		renderLog(&b, contentH)
	}

	// Fill remaining content rows
	for row := 3 + contentH; row <= termH-1; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s", row, clearToEOL()))
	}

	// Last row: help bar
	help := " ↑↓:Select  d:Delete  r:Refresh  Tab:Switch  q:Quit"
	switch tab {
	case 1:
		help = " ↑↓:Select  a:Approve  d:Deny  r:Refresh  Tab:Switch  q:Quit"
	case 2:
		help = " r:Refresh  Tab:Switch  q:Quit"
	case 3:
		help = " j/k:Scroll  Tab:Switch  q:Quit"
	}
	b.WriteString(fmt.Sprintf("\033[%d;1H\033[7m%s\033[0m%s",
		termH, help, clearToEOL()))

	// Flash message
	if msg != "" {
		b.WriteString(fmt.Sprintf("\033[%d;1H\033[33m%s\033[0m", termH-1, msg))
	}

	fmt.Print(b.String())
	needRedraw = false
}

func renderPeers(b *strings.Builder, maxLines int) {
	row := 3
	b.WriteString(fmt.Sprintf("\033[%d;1H%-4s %-20s %-14s %-6s %-8s %s",
		row, "#", "Name", "IP", "Online", "HS", clearToEOL()))
	row++

	// Cap selection
	n := len(peers.Peers)
	if n > 0 {
		if sel < 0 {
			sel = 0
		}
		if sel >= n {
			sel = n - 1
		}
	}

	start := 0
	if sel >= maxLines-1 {
		start = sel - (maxLines - 2)
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < n && row < 3+maxLines; i++ {
		p := peers.Peers[i]
		indicator := "  "
		if i == sel {
			indicator = "▶ "
		}
		online := " "
		if p.Online {
			online = "✓"
		}
		hs := handshakeAgo(p.LatestHandshake)
		name := sanitize(p.Name)
		b.WriteString(fmt.Sprintf("\033[%d;1H%s%-4d %-20s %-14s %-6s %-8s %s",
			row, indicator, i+1, trunc(name, 20), p.Address, online, hs, clearToEOL()))
		row++
	}
	for ; row < 3+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s", row, clearToEOL()))
	}
}

func renderRequests(b *strings.Builder, maxLines int) {
	row := 3
	b.WriteString(fmt.Sprintf("\033[%d;1H%-4s %-20s %-14s %-12s %-10s %s",
		row, "#", "Hostname", "IP", "Source", "Age", clearToEOL()))
	row++

	n := len(reqs.Requests)
	if n > 0 {
		if sel < 0 {
			sel = 0
		}
		if sel >= n {
			sel = n - 1
		}
	}

	for i := 0; i < n && row < 3+maxLines; i++ {
		r := reqs.Requests[i]
		indicator := "  "
		if i == sel {
			indicator = "▶ "
		}
		age := timeAgo(r.CreatedAt)
		name := sanitize(r.Hostname)
		b.WriteString(fmt.Sprintf("\033[%d;1H%s%-4d %-20s %-14s %-12s %-10s %s",
			row, indicator, i+1, trunc(name, 20), r.Address, trunc(sanitize(r.SourceIP), 12), age, clearToEOL()))
		row++
	}
	if n == 0 {
		b.WriteString(fmt.Sprintf("\033[%d;1H  (no pending requests)%s", row, clearToEOL()))
		row++
	}
	for ; row < 3+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s", row, clearToEOL()))
	}
}

func renderStatus(b *strings.Builder, maxLines int) {
	row := 3
	lines := []string{
		fmt.Sprintf("  Daemon:      %s", colorOK(status.Daemon == "running")),
		fmt.Sprintf("  WireGuard:   %s", colorOK(status.Wireguard == "ok")),
		fmt.Sprintf("  Interface:   %s (port %s)", status.Interface, status.Port),
		fmt.Sprintf("  Peers online: %d / %d", status.Online, status.Total),
		"",
		"  Peers:",
	}
	for _, line := range lines {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s%s", row, line, clearToEOL()))
		row++
	}

	for _, p := range peers.Peers {
		if row >= 3+maxLines {
			break
		}
		dot := "●"
		if p.Online {
			dot = "\033[32m●\033[0m"
		} else {
			dot = "\033[31m●\033[0m"
		}
		rx := formatBytes(p.TransferRx)
		tx := formatBytes(p.TransferTx)
		b.WriteString(fmt.Sprintf("\033[%d;1H    %s %-20s %s  rx:%s tx:%s%s",
			row, dot, trunc(sanitize(p.Name), 20), p.Address, rx, tx, clearToEOL()))
		row++
	}
	for ; row < 3+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s", row, clearToEOL()))
	}
}

func renderLog(b *strings.Builder, maxLines int) {
	start := len(logs) - maxLines
	if start < 0 {
		start = 0
	}
	if sel >= len(logs) {
		sel = len(logs) - 1
	}
	if sel < start {
		start = sel
	}
	end := start + maxLines
	if end > len(logs) {
		end = len(logs)
	}
	row := 3
	for i := start; i < end; i++ {
		line := logs[i]
		color := ""
		if strings.Contains(line, "approved") {
			color = "\033[32m"
		} else if strings.Contains(line, "rejected") || strings.Contains(line, "deleted") || strings.Contains(line, "expired") {
			color = "\033[31m"
		} else if strings.Contains(line, "submitted") {
			color = "\033[33m"
		}
		b.WriteString(fmt.Sprintf("\033[%d;1H%s%-.200s\033[0m%s", row, color, line, clearToEOL()))
		row++
	}
	for ; row < 3+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;1H%s", row, clearToEOL()))
	}
}

func handshakeAgo(s string) string {
	if s == "0" {
		return "-"
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	sec := int(time.Now().Unix()) - n
	if sec < 0 {
		return "now"
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm", sec/60)
	}
	if sec < 86400 {
		return fmt.Sprintf("%dh", sec/3600)
	}
	return fmt.Sprintf("%dd", sec/86400)
}

func timeAgo(t string) string {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return "?"
	}
	d := time.Since(parsed)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func formatBytes(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil || n == 0 {
		return "0"
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func trunc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\\", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func clearToEOL() string {
	return "\033[K"
}

func colorOK(ok bool) string {
	if ok {
		return "\033[32mok\033[0m"
	}
	return "\033[31merror\033[0m"
}
