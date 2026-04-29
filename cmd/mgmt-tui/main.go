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
	"unsafe"
)

type termios syscall.Termios

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

	tab        = 0
	sel        = 0
	running    = true
	msg        = ""
	needRedraw = true

	termW, termH = 80, 24

	boxW = 80
	ox   = 0
	oy   = 0

	oldTerm *termios
)

func main() {
	loadConfig()
	getTermSize()
	enterAltScreen()
	defer exitAltScreen()
	makeRaw()

	go keyboardLoop()
	go autoRefresh()
	refresh()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for running {
		if needRedraw {
			render()
		}
		time.Sleep(50 * time.Millisecond)
	}

	shutdown()
}

func shutdown() {
	exitAltScreen()
	restoreRaw()
	fmt.Print("\033[?25h\033[H\033[J")
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

func refresh() {
	apiPeers, _ := apiGet("/api/v1/peers")
	json.Unmarshal(apiPeers, &peers)
	apiReqs, _ := apiGet("/api/v1/requests")
	json.Unmarshal(apiReqs, &reqs)
	apiSt, _ := apiGet("/api/v1/status")
	json.Unmarshal(apiSt, &status)
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
	boxW = termW - 4
	if boxW < 50 {
		boxW = 50
	}
	if boxW > 100 {
		boxW = 100
	}
	ox = (termW - boxW) / 2
}

func getTermSize() {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err == nil {
		fmt.Sscanf(string(out), "%d %d", &termH, &termW)
	}
	if termH < 16 {
		termH = 24
	}
	if termW < 50 {
		termW = 80
	}
}

func enterAltScreen()    { fmt.Print("\033[?1049h\033[H\033[J") }
func exitAltScreen()     { fmt.Print("\033[?1049l") }
func hideCursor()        { fmt.Print("\033[?25l") }

func makeRaw() {
	fd := int(os.Stdin.Fd())
	var t termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	if err != 0 {
		return
	}
	oldTerm = new(termios)
	*oldTerm = t
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	hideCursor()
}

func restoreRaw() {
	if oldTerm == nil {
		return
	}
	fd := int(os.Stdin.Fd())
	syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(oldTerm)), 0, 0, 0)
}

func apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", cfg.APIURL+path, nil)
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiPost(path string) ([]byte, error) {
	req, _ := http.NewRequest("POST", cfg.APIURL+path, nil)
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiDelete(path string) ([]byte, error) {
	req, _ := http.NewRequest("DELETE", cfg.APIURL+path, nil)
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
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
		case b[0] == 'q' || b[0] == 'Q':
			running = false
		case b[0] == '\t':
			tab = (tab + 1) % 4
			sel = 0
			needRedraw = true
		case b[0] == 'r' || b[0] == 'R':
			msg = "Refreshed"
			refresh()
			needRedraw = true
		case n >= 3 && b[0] == 27 && b[1] == 91:
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
		case b[0] == 'j' || b[0] == 'J':
			if tab == 3 {
				sel++
				needRedraw = true
			}
		case b[0] == 'k' || b[0] == 'K':
			if tab == 3 {
				sel--
				if sel < 0 {
					sel = 0
				}
				needRedraw = true
			}
		case b[0] == 3: // Ctrl+C
			running = false
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
	body, _ := apiDelete("/api/v1/peers/" + name)
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Deleted: " + name
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

func approveRequest(id string) {
	body, _ := apiPost("/api/v1/requests/" + id + "/approve")
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Approved"
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

func denyRequest(id string) {
	body, _ := apiDelete("/api/v1/requests/" + id)
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Denied"
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

func render() {
	var b strings.Builder
	b.Grow(termW * termH * 4)

	innerW := boxW - 4

	// ── Top border ──
	b.WriteString(fmt.Sprintf("\033[%d;%dH", 1, ox))
	b.WriteString("╔══ WG-Manager ")
	b.WriteString(fmt.Sprintf(" ═ %d peers │ %d pending │ online %d/%d ",
		len(peers.Peers), len(reqs.Requests), status.Online, len(peers.Peers)))
	remain := boxW - 2 - 15 - len(fmt.Sprintf(" %d peers │ %d pending │ online %d/%d ",
		len(peers.Peers), len(reqs.Requests), status.Online, len(peers.Peers)))
	if remain < 2 {
		remain = 2
	}
	for i := 0; i < remain; i++ {
		b.WriteString("═")
	}
	b.WriteString("╗")

	// ── Tab bar ──
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", 2, ox))
	tabLeft := ox + 1
	tabs := []string{" Peers ", " Requests ", " Status ", " Log "}
	for i, t := range tabs {
		b.WriteString(fmt.Sprintf("\033[%d;%dH", 2, tabLeft))
		if i == tab {
			b.WriteString(fmt.Sprintf("\033[7m%s\033[0m", t))
		} else {
			b.WriteString(t)
		}
		tabLeft += len(t)
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", 2, ox+boxW-1))

	// ── Line after tabs ──
	b.WriteString(fmt.Sprintf("\033[%d;%dH╟", 3, ox))
	for i := 0; i < boxW-2; i++ {
		b.WriteString("─")
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH╢", 3, ox+boxW-1))

	// ── Content ──
	contentStartRow := 4
	contentEndRow := termH - 2
	contentH := contentEndRow - contentStartRow
	if contentH < 4 {
		contentH = 4
	}

	// Cap selection
	switch tab {
	case 0:
		if n := len(peers.Peers); n > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= n {
				sel = n - 1
			}
		}
	case 1:
		if n := len(reqs.Requests); n > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= n {
				sel = n - 1
			}
		}
	case 3:
		if n := len(logs); n > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= n {
				sel = n - 1
			}
		}
	}

	switch tab {
	case 0:
		renderPeers(&b, contentStartRow, contentH, ox+1, innerW)
	case 1:
		renderRequests(&b, contentStartRow, contentH, ox+1, innerW)
	case 2:
		renderStatus(&b, contentStartRow, contentH, ox+1, innerW)
	case 3:
		renderLog(&b, contentStartRow, contentH, ox+1, innerW)
	}

	// ── Fill remaining lines with side borders ──
	usedRow := contentStartRow
	switch tab {
	case 0:
		if len(peers.Peers) > 0 {
			usedRow = contentStartRow + len(peers.Peers)
		}
		if usedRow >= contentEndRow {
			usedRow = contentStartRow + 1
		}
	case 1:
		if len(reqs.Requests) > 0 {
			usedRow = contentStartRow + len(reqs.Requests)
		}
		if usedRow >= contentEndRow {
			usedRow = contentStartRow + 1
		}
	case 2:
		usedRow = contentStartRow + 6
		if len(peers.Peers) > 0 {
			usedRow += len(peers.Peers) + 1
		}
	case 3:
		if len(logs) > 0 {
			usedRow = contentStartRow + len(logs)
		}
		if usedRow >= contentEndRow {
			usedRow = contentStartRow + 1
		}
	}
	for r := usedRow; r <= contentEndRow; r++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", r, ox))
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", r, ox+boxW-1))
	}
	// Also fill rows that were NOT used by content
	// Already handled by the border fill above

	// ── Bottom border ──
	bottomRow := termH - 1
	b.WriteString(fmt.Sprintf("\033[%d;%dH╚", bottomRow, ox))
	help := " Tab:Switch  ↑↓:Select "
	switch tab {
	case 1:
		help += " a:Approve  d:Deny "
	case 0:
		help += " d:Delete "
	case 2:
		help += " "
	case 3:
		help += " j/k:Scroll "
	}
	help += " r:Refresh  q:Quit "
	helpText := help
	b.WriteString(helpText)
	remainHelp := boxW - 2 - len(helpText)
	if remainHelp < 2 {
		remainHelp = 2
	}
	for i := 0; i < remainHelp; i++ {
		b.WriteString("═")
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH╝", bottomRow, ox+boxW-1))

	// ── Message ──
	if msg != "" {
		b.WriteString(fmt.Sprintf("\033[%d;%dH\033[33m%s\033[0m", bottomRow-1, ox+1, msg))
	}

	fmt.Print(b.String())
	needRedraw = false
}

func renderPeers(b *strings.Builder, startRow, maxLines, left, width int) {
	row := startRow
	b.WriteString(fmt.Sprintf("\033[%d;%dH║ %-4s %-22s %-14s %-5s %-8s %s",
		row, left, "#", "Name", "IP", "On", "HS", ""))
	padEnd(b, row, left+width-2, width)
	row++

	for i, p := range peers.Peers {
		if row >= startRow+maxLines {
			break
		}
		indicator := "  "
		if i == sel {
			indicator = "▶ "
		}
		online := " "
		if p.Online {
			online = "✓"
		}
		hs := handshakeAgo(p.LatestHandshake)
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s%-4d %-22s %-14s %-5s %-8s",
			row, left, indicator, i+1, trunc(sanitize(p.Name), 22), p.Address, online, hs))
		padEnd(b, row, left+width-2, width)
		row++
	}
	for ; row < startRow+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, left))
		padEnd(b, row, left+width-2, width)
	}
}

func renderRequests(b *strings.Builder, startRow, maxLines, left, width int) {
	row := startRow
	b.WriteString(fmt.Sprintf("\033[%d;%dH║ %-4s %-22s %-14s %-12s %-10s",
		row, left, "#", "Hostname", "IP", "Source", "Age"))
	padEnd(b, row, left+width-2, width)
	row++

	for i, r := range reqs.Requests {
		if row >= startRow+maxLines {
			break
		}
		indicator := "  "
		if i == sel {
			indicator = "▶ "
		}
		age := timeAgo(r.CreatedAt)
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s%-4d %-22s %-14s %-12s %-10s",
			row, left, indicator, i+1, trunc(sanitize(r.Hostname), 22), r.Address, trunc(sanitize(r.SourceIP), 12), age))
		padEnd(b, row, left+width-2, width)
		row++
	}
	if len(reqs.Requests) == 0 {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║   (no pending requests)", row, left))
		padEnd(b, row, left+width-2, width)
		row++
	}
	for ; row < startRow+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, left))
		padEnd(b, row, left+width-2, width)
	}
}

func renderStatus(b *strings.Builder, startRow, maxLines, left, width int) {
	row := startRow
	lines := []string{
		fmt.Sprintf("║   Daemon:       %s", colorOK(status.Daemon == "running")),
		fmt.Sprintf("║   WireGuard:    %s", colorOK(status.Wireguard == "ok")),
		fmt.Sprintf("║   Interface:    %s  port %s", status.Interface, status.Port),
		fmt.Sprintf("║   Peers online: %d / %d", status.Online, status.Total),
		"║",
		"║   Peers:",
	}
	for _, line := range lines {
		if row >= startRow+maxLines {
			break
		}
		b.WriteString(fmt.Sprintf("\033[%d;%dH%s", row, left, line))
		padEnd(b, row, left+width-2, width)
		row++
	}
	for _, p := range peers.Peers {
		if row >= startRow+maxLines {
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
		b.WriteString(fmt.Sprintf("\033[%d;%dH║     %s %-20s %s  rx:%s tx:%s",
			row, left, dot, trunc(sanitize(p.Name), 20), p.Address, rx, tx))
		padEnd(b, row, left+width-2, width)
		row++
	}
	for ; row < startRow+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, left))
		padEnd(b, row, left+width-2, width)
	}
}

func renderLog(b *strings.Builder, startRow, maxLines, left, width int) {
	offset := sel
	if offset < 0 {
		offset = 0
	}
	if offset > len(logs)-maxLines {
		offset = len(logs) - maxLines
	}
	if offset < 0 {
		offset = 0
	}
	row := startRow
	for i := offset; i < len(logs) && row < startRow+maxLines; i++ {
		line := logs[i]
		color := ""
		if strings.Contains(line, "approved") {
			color = "\033[32m"
		} else if strings.Contains(line, "rejected") || strings.Contains(line, "deleted") || strings.Contains(line, "expired") {
			color = "\033[31m"
		} else if strings.Contains(line, "submitted") {
			color = "\033[33m"
		}
		prefix := "║  "
		if i == sel {
			prefix = "║ ▶"
		}
		b.WriteString(fmt.Sprintf("\033[%d;%dH%s%s%s\033[0m",
			row, left, prefix, color, line))
		padEnd(b, row, left+width-2, width)
		row++
	}
	for ; row < startRow+maxLines; row++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, left))
		padEnd(b, row, left+width-2, width)
	}
}

func padEnd(b *strings.Builder, row, col, width int) {
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, col+1))
}

func handshakeAgo(s string) string {
	if s == "0" {
		return "-"
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return "?"
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
	return strings.Map(func(r rune) rune {
		if r == '\\' || r == '\r' || r == '\n' || r < ' ' {
			return -1
		}
		return r
	}, s)
}

func colorOK(ok bool) string {
	if ok {
		return "\033[32mok\033[0m"
	}
	return "\033[31merror\033[0m"
}
