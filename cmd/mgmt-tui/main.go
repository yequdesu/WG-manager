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
	oldTerm      *termios
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
	if termW < 60 {
		termW = 80
	}
}

func enterAltScreen()  { fmt.Print("\033[?1049h\033[H\033[J") }
func exitAltScreen()   { fmt.Print("\033[?1049l") }
func hideCursor()      { fmt.Print("\033[?25l") }

func makeRaw() {
	fd := int(os.Stdin.Fd())
	var t termios
	_, _, e := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	if e != 0 {
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
				doDelete()
			case tab == 1 && sel < len(reqs.Requests):
				doDeny()
			}
		case b[0] == 'a' || b[0] == 'A':
			if tab == 1 && sel < len(reqs.Requests) {
				doApprove()
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
		case b[0] == 3:
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

func doDelete() {
	body, _ := apiDelete("/api/v1/peers/" + peers.Peers[sel].Name)
	var r struct{ Success bool; Error string }
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Deleted: " + peers.Peers[sel].Name
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

func doApprove() {
	body, _ := apiPost("/api/v1/requests/" + reqs.Requests[sel].ID + "/approve")
	var r struct{ Success bool; Error string }
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Approved"
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

func doDeny() {
	body, _ := apiDelete("/api/v1/requests/" + reqs.Requests[sel].ID)
	var r struct{ Success bool; Error string }
	json.Unmarshal(body, &r)
	if r.Success {
		msg = "Denied"
	} else {
		msg = "Error: " + r.Error
	}
	refresh()
	needRedraw = true
}

// ═══════════════════════════════════════════════════════════
//  Render engine
// ═══════════════════════════════════════════════════════════

type frame struct {
	bw, bh, bx, by int   // box width, height, x-offset, y-start
	cw              int   // content width (inside box)
	cs, ce          int   // content start/end row
}

func (f *frame) left() int  { return f.bx + 1 }
func (f *frame) right() int { return f.bx + f.bw - 1 }

func calcFrame() frame {
	f := frame{}
	f.bw = termW - 2
	if f.bw < 56 {
		f.bw = 56
	}
	if f.bw > 110 {
		f.bw = 110
	}
	f.bh = termH
	f.by = 1
	f.bx = (termW - f.bw) / 2
	if f.bx < 0 {
		f.bx = 0
	}
	f.cw = f.bw - 4
	f.cs = 4
	f.ce = f.by + f.bh - 2
	return f
}

func render() {
	f := calcFrame()

	nPeers := len(peers.Peers)
	nReqs := len(reqs.Requests)

	var b strings.Builder
	b.Grow(termW * termH * 4)

	// Row 1: top border + title
	title := fmt.Sprintf(" WG-Manager  │ %d peers  │ %d pending  │ online %d/%d",
		nPeers, nReqs, status.Online, nPeers)
	b.WriteString(fmt.Sprintf("\033[%d;%dH\033[7m╔%s%s╗\033[0m",
		f.by, f.bx, title, repeat("═", f.bw-2-lenAnsi(0, title))))

	// Row 2: tab bar
	b.WriteString(fmt.Sprintf("\033[%d;%dH║\033[0m", f.by+1, f.bx))
	tabs := []string{" Peers ", " Requests ", " Status ", " Log "}
	pos := f.left()
	for i, t := range tabs {
		b.WriteString(fmt.Sprintf("\033[%d;%dH", f.by+1, pos))
		if i == tab {
			b.WriteString(fmt.Sprintf("\033[7m%s\033[0m", t))
		} else {
			b.WriteString(t)
		}
		pos += len(t)
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH║\033[0m", f.by+1, f.right()))

	// Row 3: separator
	b.WriteString(fmt.Sprintf("\033[%d;%dH╟%s╢\033[0m",
		f.by+2, f.bx, repeat("─", f.bw-2)))

	// ── Clear all content rows ──
	for r := f.cs; r <= f.ce; r++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║\033[%d;%dH║",
			r, f.bx, r, f.right()))
	}

	// Cap selection
	switch tab {
	case 0:
		if nPeers > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= nPeers {
				sel = nPeers - 1
			}
		}
	case 1:
		if nReqs > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= nReqs {
				sel = nReqs - 1
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

	// Render tab content
	switch tab {
	case 0:
		renderPeers(&b, f)
	case 1:
		renderRequests(&b, f)
	case 2:
		renderStatus(&b, f)
	case 3:
		renderLog(&b, f)
	}

	// Bottom border
	bottom := f.by + f.bh - 1
	help := " Tab:Switch  ↑↓:Select "
	switch tab {
	case 1:
		help += " a:Approve  d:Deny "
	case 0:
		help += " d:Delete "
	case 3:
		help += " j/k:Scroll "
	}
	help += " r:Refresh  q:Quit "
	remain := f.bw - 2 - len(help)
	if remain < 0 {
		remain = 0
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH╚%s%s╝\033[0m",
		bottom, f.bx, help, repeat("═", remain)))

	// Message
	if msg != "" {
		b.WriteString(fmt.Sprintf("\033[%d;%dH\033[33m%s\033[0m", bottom-1, f.left(), msg))
	}

	fmt.Print(b.String())
	needRedraw = false
}

func renderPeers(b *strings.Builder, f frame) {
	row := f.cs
	// Clamp selection
	n := len(peers.Peers)
	if n > 0 {
		if sel >= n {
			sel = n - 1
		}
		if sel < 0 {
			sel = 0
		}
	}

	// Header
	b.WriteString(fmt.Sprintf("\033[%d;%dH║ %-4s %-20s %-14s %-5s %-8s ║",
		row, f.bx, "#", "Name", "IP", "On", "HS"))
	row++

	for i, p := range peers.Peers {
		if row > f.ce {
			break
		}
		ind := "  "
		if i == sel {
			ind = "▶ "
		}
		on := " "
		if p.Online {
			on = "\033[32m✓\033[0m"
		}
		hs := handshakeAgo(p.LatestHandshake)
		line := fmt.Sprintf("%s%-4d %-20s %-14s %-5s %-8s",
			ind, i+1, trunc(sanitize(p.Name), 20), p.Address, on, hs)
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s ║", row, f.bx, padRightAnsi(line, f.cw-2)))
		row++
	}
}

func renderRequests(b *strings.Builder, f frame) {
	row := f.cs
	n := len(reqs.Requests)
	if n > 0 {
		if sel >= n {
			sel = n - 1
		}
		if sel < 0 {
			sel = 0
		}
	}

	b.WriteString(fmt.Sprintf("\033[%d;%dH║ %-4s %-22s %-14s %-12s %-10s ║",
		row, f.bx, "#", "Hostname", "IP", "Source", "Age"))
	row++

	for i, r := range reqs.Requests {
		if row > f.ce {
			break
		}
		ind := "  "
		if i == sel {
			ind = "▶ "
		}
		age := timeAgo(r.CreatedAt)
		line := fmt.Sprintf("%s%-4d %-22s %-14s %-12s %-10s",
			ind, i+1, trunc(sanitize(r.Hostname), 22), r.Address, trunc(sanitize(r.SourceIP), 12), age)
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s ║", row, f.bx, padRightAnsi(line, f.cw-2)))
		row++
	}
	if n == 0 {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║   (no pending requests) ║", row, f.bx))
	}
}

func renderStatus(b *strings.Builder, f frame) {
	row := f.cs
	items := []string{
		fmt.Sprintf("║   Daemon:       %s", colorOK(status.Daemon == "running")),
		fmt.Sprintf("║   WireGuard:    %s", colorOK(status.Wireguard == "ok")),
		fmt.Sprintf("║   Interface:    %s  port %s", status.Interface, status.Port),
		fmt.Sprintf("║   Peers online: %d / %d", status.Online, status.Total),
		"║",
		"║   Peers:",
	}
	for _, it := range items {
		if row > f.ce {
			break
		}
		b.WriteString(fmt.Sprintf("\033[%d;%dH%s ║", row, f.bx, padRightAnsi(it, f.cw-1)))
		row++
	}
	for _, p := range peers.Peers {
		if row > f.ce {
			break
		}
		dot := "\033[31m●\033[0m"
		if p.Online {
			dot = "\033[32m●\033[0m"
		}
		rx := formatBytes(p.TransferRx)
		tx := formatBytes(p.TransferTx)
		line := fmt.Sprintf("║     %s %-20s %s  rx:%s tx:%s",
			dot, trunc(sanitize(p.Name), 20), p.Address, rx, tx)
		b.WriteString(fmt.Sprintf("\033[%d;%dH%s ║", row, f.bx, padRightAnsi(line, f.cw-1)))
		row++
	}
}

func renderLog(b *strings.Builder, f frame) {
	n := len(logs)
	max := f.ce - f.cs
	if max < 0 {
		max = 0
	}
	if sel < 0 {
		sel = 0
	}
	if sel > n-1 {
		sel = n - 1
	}
	start := sel - max/2
	if start < 0 {
		start = 0
	}
	row := f.cs
	for i := start; i < n && row <= f.ce; i++ {
		line := logs[i]
		color := ""
		if strings.Contains(line, "approved") {
			color = "\033[32m"
		} else if strings.Contains(line, "rejected") || strings.Contains(line, "deleted") || strings.Contains(line, "expired") {
			color = "\033[31m"
		} else if strings.Contains(line, "submitted") {
			color = "\033[33m"
		}
		prefix := "  "
		if i == sel {
			prefix = "▶ "
		}
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s%s%s\033[0m ║",
			row, f.bx, prefix, color, padRight(line, f.cw-len(prefix)-4)))
		row++
	}
}

// ═══════════════════════════════════════════════════════════
//  Helpers
// ═══════════════════════════════════════════════════════════

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

func padRightAnsi(s string, width int) string {
	visible := stripAnsi(s)
	if len(visible) >= width {
		return s[:len(s)-(len(visible)-width)]
	}
	return s + strings.Repeat(" ", width-len(visible))
}

func stripAnsi(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lenAnsi(code int, s string) int {
	return len(stripAnsi(s)) + code
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
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "\u2026"
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
