//go:build linux

package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"wire-guard-dev/cmd/mgmt-tui/bubble"
)

func main() {
	style := "classic"
	legacy := false

	for _, arg := range os.Args[1:] {
		switch {
		case arg == "--legacy":
			legacy = true
		case arg == "--style=dashboard" || arg == "--style=dash":
			style = "dashboard"
		case arg == "--style=minimal" || arg == "--style=mini":
			style = "minimal"
		case arg == "--style=classic":
			style = "classic"
		}
	}

	if legacy {
		runLegacy()
		return
	}

	loadConfig()

	if cfg.APIURL == "" {
		fmt.Fprintf(os.Stderr, "Error: cannot determine API URL from config\n")
		os.Exit(1)
	}

	theme := bubble.ThemeByName(style)
	styleIdx := bubble.StyleClassic
	switch style {
	case "dashboard":
		styleIdx = bubble.StyleDashboard
	case "minimal":
		styleIdx = bubble.StyleMinimal
	}

	m := bubble.NewModel(bubble.Config{
		APIURL:   cfg.APIURL,
		APIKey:   cfg.APIKey,
		AuditLog: cfg.AuditLog,
	}, theme, styleIdx)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
