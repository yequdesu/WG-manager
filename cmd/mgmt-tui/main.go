//go:build linux

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
	"unicode/utf8"
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
	if cfg.AuditLog == "" {
		cfg.AuditLog = "/var/log/wg-mgmt/audit.log"
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
//  Render
// ═══════════════════════════════════════════════════════════

type frame struct {
	bw, bx, by int
	cw         int // space between left-║ and right-║
	cs, ce     int
}

func (f frame) leftCol() int  { return f.bx }
func (f frame) rightCol() int { return f.bx + f.bw - 1 }
func (f frame) innerCol() int { return f.bx + 1 }

func calcFrame() frame {
	f := frame{}
	f.bw = termW - 2
	if f.bw < 50 {
		f.bw = 50
	}
	if f.bw > 110 {
		f.bw = 110
	}
	f.by = 1
	f.bx = (termW - f.bw) / 2
	if f.bx < 0 {
		f.bx = 0
	}
	f.cw = f.bw - 2
	f.cs = 4
	f.ce = termH - 1 // row above bottom bar
	return f
}

func render() {
	f := calcFrame()

	var b strings.Builder
	b.Grow(termW * termH * 4)

	// Row 1: top border + title
	title := fmt.Sprintf(" WG-Manager  %d peers  %d pending  online %d/%d ",
		len(peers.Peers), len(reqs.Requests), status.Online, len(peers.Peers))
	b.WriteString(fmt.Sprintf("\033[%d;%dH╔%s%s╗",
		f.by, f.leftCol(), title, fill("═", f.cw-len(title))))

	// Row 2: tab bar
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", f.by+1, f.leftCol()))
	index := f.innerCol()
	tabs := []string{" Peers ", " Requests ", " Status ", " Log "}
	for i, t := range tabs {
		b.WriteString(fmt.Sprintf("\033[%d;%dH", f.by+1, index))
		if i == tab {
			b.WriteString(fmt.Sprintf("\033[7m%s\033[0m", t))
		} else {
			b.WriteString(t)
		}
		index += len(t)
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", f.by+1, f.rightCol()))

	// Row 3: separator
	b.WriteString(fmt.Sprintf("\033[%d;%dH╟%s╢",
		f.by+2, f.leftCol(), fill("─", f.cw)))

	// Clear all content rows (║ + spaces + ║)
	for r := f.cs; r <= f.ce; r++ {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║%s║", r, f.leftCol(), fill(" ", f.cw)))
	}

	// Cap selection
	switch tab {
	case 0:
		n := len(peers.Peers)
		if n > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= n {
				sel = n - 1
			}
		}
	case 1:
		n := len(reqs.Requests)
		if n > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= n {
				sel = n - 1
			}
		}
	case 3:
		n := len(logs)
		if n > 0 {
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
		renderPeers(&b, f)
	case 1:
		renderRequests(&b, f)
	case 2:
		renderStatus(&b, f)
	case 3:
		renderLog(&b, f)
	}

	// Bottom bar
	bottom := f.by + termH - 1
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
	rem := f.cw - utf8.RuneCountInString(help)
	if rem < 0 {
		rem = 0
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH╚%s%s╝",
		bottom, f.leftCol(), help, fill("═", rem)))

	if msg != "" {
		b.WriteString(fmt.Sprintf("\033[%d;%dH\033[33m%s\033[0m", bottom-1, f.innerCol(), msg))
	}

	fmt.Print(b.String())
	needRedraw = false
}

// writeRow emits: ║ <content padded to cw> ║
// Right border uses absolute cursor position (never drifts).
func writeRow(b *strings.Builder, f frame, row int, content string, selected bool) {
	l := f.leftCol()
	r := f.rightCol()
	if selected {
		// Invert whole row, right border separately for safety
		b.WriteString(fmt.Sprintf("\033[%d;%dH\033[7m║ %s \033[0m", row, l, padRight(content, f.cw-2)))
	} else {
		b.WriteString(fmt.Sprintf("\033[%d;%dH║ %s ", row, l, padRight(content, f.cw-2)))
	}
	b.WriteString(fmt.Sprintf("\033[%d;%dH║", row, r))
}

func renderPeers(b *strings.Builder, f frame) {
	row := f.cs
	n := len(peers.Peers)
	if n > 0 {
		if sel < 0 { sel = 0 }
		if sel >= n { sel = n - 1 }
	}
	writeRow(b, f, row, fmt.Sprintf("%-4s %-20s %-14s %-7s %-5s",
		"#", "Name", "IP", "Status", "HS"), false)
	row++

	listEnd := row
	for i, p := range peers.Peers {
		if row > f.ce { break }
		ind := "  "
		if i == sel { ind = "> " }
		on := "OFFLINE"
		if p.Online { on = "ONLINE" }
		hs := handshakeAgo(p.LatestHandshake)
		writeRow(b, f, row,
			fmt.Sprintf("%s%-4d %-20s %-14s %-7s %-5s",
				ind, i+1, trunc(sanitize(p.Name), 20), p.Address, on, hs),
			i == sel)
		row++
	}
	listEnd = row

	// Detail panel for selected peer
	if n > 0 && sel < n && listEnd+6 <= f.ce {
		p := peers.Peers[sel]
		writeRow(b, f, listEnd, "  ── Details ───────────────────────────────────────", false)
		listEnd++
		writeRow(b, f, listEnd, fmt.Sprintf("  Name:      %s", sanitize(p.Name)), false); listEnd++
		writeRow(b, f, listEnd, fmt.Sprintf("  IP:        %s", p.Address), false); listEnd++
		writeRow(b, f, listEnd, fmt.Sprintf("  PublicKey: %s", trunc(p.PublicKey, 50)), false); listEnd++
		endpoint := p.Endpoint
		if endpoint == "(none)" { endpoint = "—" }
		writeRow(b, f, listEnd, fmt.Sprintf("  Endpoint:  %s", endpoint), false); listEnd++
		hs := handshakeAgo(p.LatestHandshake)
		rx := formatBytes(p.TransferRx); tx := formatBytes(p.TransferTx)
		writeRow(b, f, listEnd, fmt.Sprintf("  HS: %s  rx:%s  tx:%s", hs, rx, tx), false); listEnd++
		if p.DNS != "" {
			writeRow(b, f, listEnd, fmt.Sprintf("  DNS: %s", p.DNS), false); listEnd++
		}
	}
}

func renderRequests(b *strings.Builder, f frame) {
	row := f.cs
	n := len(reqs.Requests)
	if n > 0 {
		if sel < 0 { sel = 0 }
		if sel >= n { sel = n - 1 }
	}
	writeRow(b, f, row, fmt.Sprintf("%-4s %-22s %-14s %-12s %-8s",
		"#", "Hostname", "IP", "Source", "Age"), false)
	row++
	for i, r := range reqs.Requests {
		if row > f.ce { break }
		ind := "  "
		if i == sel { ind = "> " }
		age := timeAgo(r.CreatedAt)
		writeRow(b, f, row,
			fmt.Sprintf("%s%-4d %-22s %-14s %-12s %-8s",
				ind, i+1, trunc(sanitize(r.Hostname), 22), r.Address, trunc(sanitize(r.SourceIP), 12), age),
			i == sel)
		row++
	}
	if n == 0 {
		writeRow(b, f, row, "  (no pending requests)", false)
	}
}

func renderStatus(b *strings.Builder, f frame) {
	row := f.cs
	for _, line := range []string{
		fmt.Sprintf("  Daemon:       %s", onOff(status.Daemon == "running")),
		fmt.Sprintf("  WireGuard:    %s", onOff(status.Wireguard == "ok")),
		fmt.Sprintf("  Interface:    %s  port %s", status.Interface, status.Port),
		fmt.Sprintf("  Peers online: %d / %d", status.Online, status.Total),
		"",
		"  Peers:",
	} {
		if row > f.ce { break }
		writeRow(b, f, row, line, false)
		row++
	}
	for _, p := range peers.Peers {
		if row > f.ce { break }
		dot := "x"
		if p.Online { dot = "o" }
		rx := formatBytes(p.TransferRx)
		tx := formatBytes(p.TransferTx)
		writeRow(b, f, row,
			fmt.Sprintf("    %s %-20s %s  rx:%s tx:%s",
				dot, trunc(sanitize(p.Name), 20), p.Address, rx, tx), false)
		row++
	}
}

func renderLog(b *strings.Builder, f frame) {
	n := len(logs)
	max := f.ce - f.cs
	if max < 0 { max = 0 }
	if sel < 0 { sel = 0 }
	if sel >= n { sel = n - 1 }
	start := sel - max/2
	if start < 0 { start = 0 }
	if start > n-max && n > max { start = n - max }
	row := f.cs
	if n == 0 {
		writeRow(b, f, row, "  (no events yet — peer join or admin action to populate)", false)
	} else {
		for i := start; i < n && row <= f.ce; i++ {
			prefix := "  "
			if i == sel { prefix = "> " }
			writeRow(b, f, row,
				fmt.Sprintf("%s%s", prefix, logs[i]),
				i == sel)
			row++
		}
	}
}

// ═══════════════════════════════════════════════════════════
//  Pure-text helpers (no inline ANSI in content)
// ═══════════════════════════════════════════════════════════

func padRight(s string, w int) string {
	if utf8.RuneCountInString(s) >= w {
		runes := []rune(s)
		if len(runes) > w {
			return string(runes[:w])
		}
		return s
	}
	return s + strings.Repeat(" ", w-utf8.RuneCountInString(s))
}

func fill(ch string, n int) string {
	if n <= 0 { return "" }
	return strings.Repeat(ch, n)
}

func handshakeAgo(s string) string {
	if s == "0" { return "-" }
	n, err := strconv.Atoi(s)
	if err != nil { return "?" }
	sec := int(time.Now().Unix()) - n
	if sec < 0 { return "now" }
	if sec < 60 { return fmt.Sprintf("%ds", sec) }
	if sec < 3600 { return fmt.Sprintf("%dm", sec/60) }
	if sec < 86400 { return fmt.Sprintf("%dh", sec/3600) }
	return fmt.Sprintf("%dd", sec/86400)
}

func timeAgo(t string) string {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil { return "?" }
	d := time.Since(parsed)
	if d < time.Minute { return fmt.Sprintf("%ds", int(d.Seconds())) }
	if d < time.Hour { return fmt.Sprintf("%dm", int(d.Minutes())) }
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func formatBytes(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil || n == 0 { return "0" }
	switch {
	case n >= 1<<30: return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20: return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10: return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func onOff(ok bool) string {
	if ok { return "OK" }
	return "ERROR"
}

func trunc(s string, max int) string {
	r := []rune(s)
	if len(r) <= max { return s }
	return string(r[:max-1]) + "\u2026"
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\\' || r == '\r' || r == '\n' || r < ' ' { return -1 }
		return r
	}, s)
}
