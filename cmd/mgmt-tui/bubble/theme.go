package bubble

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	refreshInterval = 5 * time.Second

	StyleClassic   = 0
	StyleDashboard = 1
	StyleMinimal   = 2
	StyleWindow    = 3
)

type Theme struct {
	Name      string
	Base      lipgloss.Style
	Title     lipgloss.Style
	Tab       lipgloss.Style
	TabActive lipgloss.Style
	TabBar    lipgloss.Style
	Panel     lipgloss.Style
	Online    lipgloss.Style
	Offline   lipgloss.Style
	Selected  lipgloss.Style
	Help      lipgloss.Style
	Status    lipgloss.Style
	Cursor    string
	TableHdr  lipgloss.Style
	TableRow  lipgloss.Style
	SparkHi   lipgloss.Style
	SparkLo   lipgloss.Style
	Desktop   lipgloss.Style
	WinFrame  lipgloss.Style
	WinTitle  lipgloss.Style
	CloseBtn  lipgloss.Style
}

func ThemeByName(name string) Theme {
	switch name {
	case "dashboard", "dash":
		return ThemeDashboard()
	case "minimal", "mini":
		return ThemeMinimal()
	case "window", "win":
		return ThemeWindow()
	default:
		return ThemeClassic()
	}
}

func ThemeByIndex(idx int) Theme {
	switch idx {
	case StyleDashboard:
		return ThemeDashboard()
	case StyleMinimal:
		return ThemeMinimal()
	case StyleWindow:
		return ThemeWindow()
	default:
		return ThemeClassic()
	}
}

func ThemeClassic() Theme {
	return Theme{
		Name: "classic",
		Base: lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder(), true).
			BorderForeground(lipgloss.Color("63")),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("63")).
			Bold(true),
		Tab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Padding(0, 2),
		TabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("63")).
			Background(lipgloss.Color("236")).
			Padding(0, 2).
			Bold(true).
			Underline(true),
		TabBar: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color("63")),
		Panel: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1),
		Online:  lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		Offline: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Selected: lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Bold(true),
		Help:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Status:   lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true),
		Cursor:   "▸",
		TableHdr: lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true),
		TableRow: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		SparkHi:  lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		SparkLo:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
}

func ThemeDashboard() Theme {
	return Theme{
		Name: "dashboard",
		Base: lipgloss.NewStyle(),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true),
		Tab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 2),
		TabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Background(lipgloss.Color("23")).
			Padding(0, 2).
			Bold(true),
		TabBar: lipgloss.NewStyle().
			Padding(0, 1),
		Panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("23")).
			Padding(0, 1),
		Online:  lipgloss.NewStyle().Foreground(lipgloss.Color("48")),
		Offline: lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		Selected: lipgloss.NewStyle().
			Background(lipgloss.Color("23")).
			Foreground(lipgloss.Color("86")),
		Help:   lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		Status: lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true),
		Cursor: "●",
		TableHdr: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color("23")),
		TableRow: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		SparkHi:  lipgloss.NewStyle().Foreground(lipgloss.Color("48")),
		SparkLo:  lipgloss.NewStyle().Foreground(lipgloss.Color("23")),
	}
}

func ThemeMinimal() Theme {
	return Theme{
		Name: "minimal",
		Base: lipgloss.NewStyle(),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Bold(true),
		Tab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Padding(0, 2),
		TabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Padding(0, 2).
			Bold(true).
			Underline(true),
		TabBar: lipgloss.NewStyle(),
		Panel:  lipgloss.NewStyle().Padding(0, 1),
		Online: lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		Offline: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Faint(true),
		Selected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true),
		Help:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Status: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		Cursor: ">",
		TableHdr: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color("8")),
		TableRow: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		SparkHi:  lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		SparkLo:  lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Desktop:  lipgloss.NewStyle(),
		WinFrame: lipgloss.NewStyle(),
		WinTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Bold(true),
		CloseBtn: lipgloss.NewStyle(),
	}
}

func ThemeWindow() Theme {
	return Theme{
		Name: "window",
		Base:   lipgloss.NewStyle(),
		Desktop: lipgloss.NewStyle().Width(120).Height(40),
		WinFrame: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), true).
			BorderForeground(lipgloss.Color("63")).
			Background(lipgloss.Color("17")),
		WinTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("63")).
			Bold(true).
			Padding(0, 1),
		CloseBtn: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("63")).
			Padding(0, 1),
		Title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Bold(true),
		Tab: lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Background(lipgloss.Color("17")).
			Padding(0, 2),
		TabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("23")).
			Padding(0, 2).
			Bold(true),
		TabBar: lipgloss.NewStyle().Background(lipgloss.Color("17")),
		Panel: lipgloss.NewStyle().
			Background(lipgloss.Color("17")).
			Padding(0, 1),
		Online: lipgloss.NewStyle().
			Foreground(lipgloss.Color("48")).
			Background(lipgloss.Color("17")),
		Offline: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Background(lipgloss.Color("17")),
		Selected: lipgloss.NewStyle().
			Background(lipgloss.Color("23")).
			Foreground(lipgloss.Color("15")).
			Bold(true),
		Help:   lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Background(lipgloss.Color("17")),
		Status: lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Background(lipgloss.Color("17")),
		Cursor: "▸",
		TableHdr: lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Background(lipgloss.Color("17")).
			Bold(true),
		TableRow: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("17")),
		SparkHi:  lipgloss.NewStyle().Foreground(lipgloss.Color("48")).Background(lipgloss.Color("17")),
		SparkLo:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(lipgloss.Color("17")),
	}
}
