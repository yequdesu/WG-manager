package bubble

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TickMsg time.Time

type model struct {
	cfg   Config
	theme Theme
	style int

	peers     []Peer
	requests  []Request
	status    StatusData
	logs      []string
	endpoint  string

	tab       int
	cursor    int
	logOffset int
	msg       string
	width     int
	height    int
	loading   bool
	quitting  bool

	winX     int
	winY     int
	dragging bool
	dragOffX int
	dragOffY int
}

type fetchMsg struct {
	peers    []Peer
	requests []Request
	status   StatusData
	logs     []string
	endpoint string
	err      error
}

func NewModel(cfg Config, theme Theme, style int) model {
	return model{
		cfg:     cfg,
		theme:   theme,
		style:   style,
		loading: true,
		winX:    3,
		winY:    1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.cfg), tickCmd())
}

func fetchCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		var res fetchMsg
		res.peers, res.endpoint, _ = fetchPeers(cfg)
		res.requests, _ = fetchRequests(cfg)
		res.status, _ = fetchStatus(cfg)
		res.logs, _ = fetchLog(cfg)
		return res
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return TickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.style == StyleWindow {
			m.winX = (m.width - 82) / 2
			if m.winX < 1 {
				m.winX = 1
			}
			m.winY = (m.height - 24) / 2
			if m.winY < 1 {
				m.winY = 1
			}
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		if m.quitting {
			return m, tea.Quit
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			keys := []int{0, 1, 2, 3}
			for i, t := range keys {
				if t == m.tab {
					m.tab = keys[(i+1)%len(keys)]
					m.cursor = 0
					break
				}
			}
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			if m.tab == 3 {
				if m.logOffset > 0 {
					m.logOffset--
				}
			}
			return m, nil
		case "down", "j":
			max := len(m.peers)
			if m.tab == 1 {
				max = len(m.requests)
			}
			if m.cursor < max-1 {
				m.cursor++
			}
			if m.tab == 3 {
				m.logOffset++
			}
			return m, nil
		case "r":
			m.loading = true
			return m, tea.Batch(fetchCmd(m.cfg), tickCmd())
		case "d":
			return m.handleDelete()
		case "a":
			return m.handleApprove()
		case "x":
			return m.handleDeny()
		case "ctrl+t":
			m.style = (m.style + 1) % 4
			m.theme = ThemeByIndex(m.style)
			return m, nil
		}

	case fetchMsg:
		m.loading = false
		if msg.err != nil {
			m.msg = fmt.Sprintf("fetch error: %v", msg.err)
		} else {
			m.msg = ""
			m.peers = msg.peers
			m.requests = msg.requests
			m.status = msg.status
			m.logs = msg.logs
			m.endpoint = msg.endpoint
			if m.cursor >= len(m.peers) && m.tab == 0 {
				m.cursor = 0
			}
			if m.cursor >= len(m.requests) && m.tab == 1 {
				m.cursor = 0
			}
		}
		return m, tickCmd()

	case TickMsg:
		return m, tea.Batch(fetchCmd(m.cfg), tickCmd())
	}

	return m, nil
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.style != StyleWindow {
		return m, nil
	}

	mx, my := msg.X, msg.Y

	if m.dragging && msg.Action == tea.MouseActionMotion {
		m.winX = mx - m.dragOffX
		m.winY = my - m.dragOffY
		if m.winX < 0 {
			m.winX = 0
		}
		if m.winY < 0 {
			m.winY = 0
		}
		return m, nil
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	winW := 82
	titleRight := m.winX + winW - 1

	if my == m.winY && mx >= titleRight-2 && mx <= titleRight {
		m.quitting = true
		return m, tea.Quit
	}

	if my == m.winY {
		m.dragging = true
		m.dragOffX = mx - m.winX
		m.dragOffY = my - m.winY
		return m, nil
	}

	if msg.Action == tea.MouseActionRelease {
		m.dragging = false
		return m, nil
	}

	if m.dragging && msg.Action == tea.MouseActionRelease {
		m.dragging = false
	}

	tabY := m.winY + 1
	if my == tabY {
		tabX := m.winX + 2
		tabs := []string{"Peers", "Requests", "Status", "Log"}
		for i, name := range tabs {
			w := len(name) + 4
			if mx >= tabX && mx < tabX+w {
				m.tab = i
				m.cursor = 0
				return m, nil
			}
			tabX += w
		}
	}

	contentY := m.winY + 3
	if my > contentY {
		rowIdx := my - contentY - 1
		if m.tab == 0 && rowIdx >= 0 && rowIdx < len(m.peers) {
			m.cursor = rowIdx
			return m, nil
		}
		if m.tab == 1 && rowIdx >= 0 && rowIdx < len(m.requests) {
			m.cursor = rowIdx
			return m, nil
		}
	}

	return m, nil
}

func (m model) handleDelete() (tea.Model, tea.Cmd) {
	switch m.tab {
	case 0:
		if len(m.peers) == 0 || m.cursor >= len(m.peers) {
			return m, nil
		}
		p := m.peers[m.cursor]
		if err := doDeletePeer(m.cfg, p.Name); err != nil {
			m.msg = fmt.Sprintf("delete failed: %v", err)
			return m, nil
		}
		m.msg = fmt.Sprintf("deleted %s", p.Name)
		return m, fetchCmd(m.cfg)
	case 1:
		if len(m.requests) == 0 || m.cursor >= len(m.requests) {
			return m, nil
		}
		r := m.requests[m.cursor]
		if err := doDeny(m.cfg, r.ID); err != nil {
			m.msg = fmt.Sprintf("deny failed: %v", err)
			return m, nil
		}
		m.msg = fmt.Sprintf("denied %s", r.Hostname)
		return m, fetchCmd(m.cfg)
	}
	return m, nil
}

func (m model) handleApprove() (tea.Model, tea.Cmd) {
	if m.tab != 1 || len(m.requests) == 0 || m.cursor >= len(m.requests) {
		return m, nil
	}
	r := m.requests[m.cursor]
	if err := doApprove(m.cfg, r.ID); err != nil {
		m.msg = fmt.Sprintf("approve failed: %v", err)
		return m, nil
	}
	m.msg = fmt.Sprintf("approved %s", r.Hostname)
	m.cursor = 0
	return m, fetchCmd(m.cfg)
}

func (m model) handleDeny() (tea.Model, tea.Cmd) {
	if m.tab != 1 || len(m.requests) == 0 || m.cursor >= len(m.requests) {
		return m, nil
	}
	r := m.requests[m.cursor]
	if err := doDeny(m.cfg, r.ID); err != nil {
		m.msg = fmt.Sprintf("deny failed: %v", err)
		return m, nil
	}
	m.msg = fmt.Sprintf("denied %s", r.Hostname)
	if m.cursor > 0 {
		m.cursor--
	}
	return m, fetchCmd(m.cfg)
}

func (m model) View() string {
	if m.quitting {
		return m.theme.Status.Render("  goodbye")
	}
	if m.loading {
		return m.theme.Title.Render("\n  Loading...")
	}

	switch m.style {
	case StyleDashboard:
		return m.viewDashboard()
	case StyleMinimal:
		return m.viewMinimal()
	case StyleWindow:
		return m.viewWindow()
	default:
		return m.viewClassic()
	}
}

func (m model) viewWindow() string {
	winW := 82

	titleStr := m.theme.WinTitle.Render(" WG-Manager ")
	closeStr := m.theme.CloseBtn.Render(" × ")
	gap := winW - 4 - lipgloss.Width(titleStr) - lipgloss.Width(closeStr)
	if gap < 0 {
		gap = 0
	}
	titleBar := m.theme.WinTitle.Render(titleStr + strings.Repeat(" ", gap) + closeStr)

	tabs := []string{"Peers", "Requests", "Status", "Log"}
	var tabBar strings.Builder
	tabBar.WriteString(" ")
	for i, t := range tabs {
		if i == m.tab {
			tabBar.WriteString(m.theme.TabActive.Render(" " + t + " "))
		} else {
			tabBar.WriteString(m.theme.Tab.Render(" " + t + " "))
		}
	}
	tabLine := m.theme.TabBar.Render(tabBar.String())

	var content string
	switch m.tab {
	case 0:
		content = viewPeers(m.theme, m.peers, m.cursor, winW-4)
	case 1:
		content = viewRequests(m.theme, m.requests, m.cursor)
	case 2:
		content = viewStatus(m.theme, m.status, m.endpoint)
	case 3:
		content = viewLog(m.theme, m.logs, m.logOffset, 16)
	}

	help := " Tab:Switch  ↑↓:Select "
	switch m.tab {
	case 1:
		help += " a:Approve  d:Deny "
	case 0:
		help += " d:Delete "
	case 3:
		help += " j/k:Scroll "
	}
	help += " r:Refresh  Ctrl+T:Theme  q:Quit "
	helpLine := m.theme.Help.Render(" " + help)

	sections := []string{
		titleBar,
		tabLine,
		"",
		content,
		"",
		helpLine,
	}

	if m.msg != "" {
		sections = append(sections, m.theme.Status.Render("  "+m.msg))
	}

	body := strings.Join(sections, "\n")
	framed := m.theme.WinFrame.Width(winW).Render(body)

	desktopStyle := m.theme.Desktop.
		Width(m.width).
		Height(m.height)

	placeStyle := lipgloss.NewStyle().
		PaddingLeft(m.winX).
		PaddingTop(m.winY).
		Render(framed)

	return desktopStyle.Render(placeStyle)
}

func (m model) viewClassic() string {
	title := fmt.Sprintf(" WG-Manager  %d peers  %d pending  online %d/%d ",
		len(m.peers), len(m.requests), m.status.Online, len(m.peers))
	titleBar := m.theme.Title.Render(padRight(title, m.width-4))

	tabs := []string{"Peers", "Requests", "Status", "Log"}
	var tabBar strings.Builder
	for i, t := range tabs {
		if i == m.tab {
			tabBar.WriteString(m.theme.TabActive.Render(t))
		} else {
			tabBar.WriteString(m.theme.Tab.Render(t))
		}
	}
	tabLine := m.theme.TabBar.Render(tabBar.String())

	var content string
	availH := m.height - 8
	if availH < 3 {
		availH = 3
	}
	switch m.tab {
	case 0:
		content = viewPeers(m.theme, m.peers, m.cursor, m.width-6)
	case 1:
		content = viewRequests(m.theme, m.requests, m.cursor)
	case 2:
		content = viewStatus(m.theme, m.status, m.endpoint)
	case 3:
		content = viewLog(m.theme, m.logs, m.logOffset, availH)
	}

	body := content

	help := " Tab:Switch  ↑↓:Select "
	switch m.tab {
	case 1:
		help += " a:Approve  d:Deny "
	case 0:
		help += " d:Delete "
	case 3:
		help += " j/k:Scroll "
	}
	help += " r:Refresh  Ctrl+T:Theme  q:Quit "
	helpLine := m.theme.Help.Render(help)

	sections := []string{
		titleBar,
		tabLine,
		"",
		body,
		"",
		helpLine,
	}

	if m.msg != "" {
		sections = append(sections, m.theme.Status.Render("  "+m.msg))
	}

	result := strings.Join(sections, "\n")
	return m.theme.Base.Width(m.width - 2).Height(m.height).Render(result)
}

func (m model) viewDashboard() string {
	titleBar := m.theme.Title.Render(fmt.Sprintf(" WG-Manager  %s  %d peers ", m.status.Interface, len(m.peers)))

	tabs := []string{"Peers", "Requests", "Status", "Log"}
	var tabBar strings.Builder
	for i, t := range tabs {
		if i == m.tab {
			tabBar.WriteString(m.theme.TabActive.Render(t))
		} else {
			tabBar.WriteString(m.theme.Tab.Render(t))
		}
	}

	serverInfo := viewServer(m.theme, m.status)
	trafficInfo := viewTraffic(m.theme, m.peers)

	topRow := lipgloss.JoinHorizontal(lipgloss.Top,
		serverInfo,
		trafficInfo,
	)

	var content string
	switch m.tab {
	case 0:
		content = viewPeers(m.theme, m.peers, m.cursor, m.width-4)
	case 1:
		content = viewRequests(m.theme, m.requests, m.cursor)
	case 2:
		content = viewStatus(m.theme, m.status, m.endpoint)
	case 3:
		availH := m.height - 10
		if availH < 3 {
			availH = 3
		}
		content = viewLog(m.theme, m.logs, m.logOffset, availH)
	}

	help := " Tab:Switch  ↑↓:Select "
	switch m.tab {
	case 1:
		help += " a:Approve  d:Deny "
	case 0:
		help += " d:Delete "
	case 3:
		help += " j/k:Scroll "
	}
	help += " r:Refresh  Ctrl+T:Theme  q:Quit "
	helpLine := m.theme.Help.Render(help)

	sections := []string{
		titleBar,
		tabBar.String(),
		topRow,
		"",
		content,
		"",
		helpLine,
	}
	if m.msg != "" {
		sections = append(sections, m.theme.Status.Render("  "+m.msg))
	}
	return strings.Join(sections, "\n")
}

func (m model) viewMinimal() string {
	statusLine := fmt.Sprintf(" %s:%s  %d peers(%s online)",
		m.status.Interface, m.status.Port,
		len(m.peers), m.theme.Online.Render(fmt.Sprintf("%d", m.status.Online)))

	tabs := []string{"Peers", "Requests", "Status", "Log"}
	var tabBar strings.Builder
	for i, t := range tabs {
		if i == m.tab {
			tabBar.WriteString(m.theme.TabActive.Render(fmt.Sprintf("%s", t)))
		} else {
			tabBar.WriteString(m.theme.Tab.Render(t))
		}
	}

	var content string
	switch m.tab {
	case 0:
		if len(m.peers) > 0 {
			for i, p := range m.peers {
				prefix := "  "
				style := m.theme.TableRow
				if p.Online {
					prefix = m.theme.Online.Render(" ● ")
				} else {
					prefix = m.theme.Offline.Render(" ○ ")
				}
				if i == m.cursor {
					prefix = m.theme.Selected.Render(" " + m.theme.Cursor + " ")
					style = m.theme.Selected
				}
				transfer := fmt.Sprintf("%s/%s", formatBytes(p.TransferRx), formatBytes(p.TransferTx))
				content += style.Render(fmt.Sprintf("%s%-18s %-14s %12s  %s\n", prefix, p.Name, p.Address, transfer, statusLabel(p.Online)))
			}
		} else {
			content = m.theme.Offline.Render("  no peers")
		}
	case 1:
		content = viewRequests(m.theme, m.requests, m.cursor)
	case 2:
		content = viewStatus(m.theme, m.status, m.endpoint)
	case 3:
		availH := m.height - 8
		if availH < 3 {
			availH = 3
		}
		content = viewLog(m.theme, m.logs, m.logOffset, availH)
	}

	help := " [↑↓]select"
	switch m.tab {
	case 1:
		help += " [a]approve [d]deny"
	case 0:
		help += " [d]delete"
	case 3:
		help += " [j/k]scroll"
	}
	help += " [r]refresh [Ctrl+T]theme [q]quit"

	separator := m.theme.TabBar.Render(strings.Repeat("─", m.width-4))

	sections := []string{
		m.theme.Status.Render(statusLine),
		tabBar.String(),
		separator,
		content,
		separator,
		m.theme.Help.Render(help),
	}
	if m.msg != "" {
		sections = append(sections, m.theme.Status.Render("  "+m.msg))
	}
	return strings.Join(sections, "\n")
}

func statusLabel(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}
