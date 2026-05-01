package bubble

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func viewPeers(th Theme, peers []Peer, cursor int, width int) string {
	pad := func(s string, w int) string {
		if len(s) > w {
			return s[:w-1] + "…"
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	if len(peers) == 0 {
		return th.Offline.Render("  No peers connected.")
	}

	lines := []string{}

	if len(peers) > 0 {
		hdr := th.TableHdr.Render(pad(" PEER", 18) + pad("ADDRESS", 14) + pad("ENDPOINT", 24) + pad("HANDSHAKE", 12) + pad("TX/RX", 20))
		lines = append(lines, hdr)
	}

	for i, p := range peers {
		style := th.Offline
		if p.Online {
			style = th.Online
		}

		endpoint := p.Endpoint
		if endpoint == "" {
			endpoint = "─"
		}

		hs := "─"
		if p.LatestHandshake != "" && p.LatestHandshake != "0" {
			n, err := strconv.Atoi(p.LatestHandshake)
			if err == nil {
				hs = handshakeAgoStr(n)
			}
		}

		transfer := fmt.Sprintf("%s / %s", formatBytes(p.TransferRx), formatBytes(p.TransferTx))
		if p.TransferRx == "" && p.TransferTx == "" {
			transfer = "─"
		}

		row := style.Render(pad(" "+p.Name, 18) + pad(p.Address, 14) + pad(endpoint, 24) + pad(hs, 12) + pad(transfer, 20))
		if i == cursor {
			row = th.Selected.Render(pad(" "+th.Cursor+p.Name, 18) + pad(p.Address, 14) + pad(endpoint, 24) + pad(hs, 12) + pad(transfer, 20))
		}
		lines = append(lines, row)
	}

	return strings.Join(lines, "\n")
}

func viewRequests(th Theme, reqs []Request, cursor int) string {
	if len(reqs) == 0 {
		return th.Offline.Render("  No pending requests.")
	}

	pad := func(s string, w int) string {
		if len(s) > w {
			return s[:w-1] + "…"
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	hdr := th.TableHdr.Render(pad(" ID", 28) + pad("HOSTNAME", 16) + pad("ADDRESS", 14) + pad("SOURCE", 18) + pad("CREATED", 20))
	lines := []string{hdr}

	for i, r := range reqs {
		row := th.TableRow.Render(pad(" "+r.ID, 28) + pad(r.Hostname, 16) + pad(r.Address, 14) + pad(r.SourceIP, 18) + pad(r.CreatedAt, 20))
		if i == cursor {
			row = th.Selected.Render(pad(" "+th.Cursor+r.ID, 28) + pad(r.Hostname, 16) + pad(r.Address, 14) + pad(r.SourceIP, 18) + pad(r.CreatedAt, 20))
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func viewStatus(th Theme, s StatusData, endpoint string) string {
	var b strings.Builder
	b.WriteString(th.Title.Render(" Server "))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Interface:    %s\n", s.Interface))
	b.WriteString(fmt.Sprintf("  Port:         %s/UDP\n", s.Port))
	b.WriteString(fmt.Sprintf("  Daemon:       %s\n", colStatus(th, s.Daemon == "running")))
	b.WriteString(fmt.Sprintf("  WireGuard:    %s\n", colStatus(th, s.Wireguard == "ok")))
	b.WriteString(fmt.Sprintf("  Online:       %d/%d\n", s.Online, s.Total))
	b.WriteString(fmt.Sprintf("  Endpoint:     %s\n", endpoint))
	return b.String()
}

func viewLog(th Theme, logs []string, offset int, height int) string {
	start := len(logs) - height
	if start < 0 {
		start = 0
	}
	start -= offset
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(logs) {
		end = len(logs)
	}

	lines := logs[start:end]
	if len(lines) == 0 {
		return th.Offline.Render("  No log entries.")
	}
	return th.TableRow.Render(strings.Join(lines, "\n"))
}

func formatBytes(s string) string {
	if s == "" || s == "0" {
		return "0"
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func handshakeAgoStr(unix int) string {
	sec := int(time.Now().Unix()) - unix
	if sec < 0 {
		return "─"
	}
	switch {
	case sec < 60:
		return "now"
	case sec < 3600:
		return fmt.Sprintf("%dm", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%dh", sec/3600)
	default:
		return fmt.Sprintf("%dd", sec/86400)
	}
}

func colStatus(th Theme, ok bool) string {
	if ok {
		return th.Online.Render("UP")
	}
	return th.Offline.Render("DOWN")
}

func viewServer(th Theme, s StatusData) string {
	var b strings.Builder
	b.WriteString(th.Panel.Render(fmt.Sprintf("%s:%s  %s online", s.Interface, s.Port, th.Online.Render(fmt.Sprintf("%d", s.Online)))))
	return b.String()
}

func viewTraffic(th Theme, peers []Peer) string {
	var rxTotal, txTotal int64
	for _, p := range peers {
		if p.TransferRx != "" {
			n, _ := strconv.ParseInt(p.TransferRx, 10, 64)
			rxTotal += n
		}
		if p.TransferTx != "" {
			n, _ := strconv.ParseInt(p.TransferTx, 10, 64)
			txTotal += n
		}
	}
	return th.Panel.Render(fmt.Sprintf("↓ %s  ↑ %s", formatBytes(fmt.Sprintf("%d", rxTotal)), formatBytes(fmt.Sprintf("%d", txTotal))))
}

func renderSparkThumb(th Theme, rx, tx string, online bool) string {
	if !online {
		return th.SparkLo.Render("──")
	}
	bars := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	rxN, _ := strconv.ParseInt(rx, 10, 64)
	txN, _ := strconv.ParseInt(tx, 10, 64)
	total := rxN + txN
	level := 0
	switch {
	case total > 1<<30:
		level = 7
	case total > 100<<20:
		level = 6
	case total > 10<<20:
		level = 5
	case total > 1<<20:
		level = 4
	case total > 100<<10:
		level = 3
	case total > 10<<10:
		level = 2
	case total > 0:
		level = 1
	}
	return th.SparkHi.Render(bars[level])
}

func padRight(s string, w int) string {
	for lipgloss.Width(s) < w {
		s += " "
	}
	if lipgloss.Width(s) > w {
		runes := []rune(s)
		return string(runes[:w])
	}
	return s
}

func padLeft(s string, w int) string {
	for lipgloss.Width(s) < w {
		s = " " + s
	}
	return s
}
