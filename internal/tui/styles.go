package tui

import "charm.land/lipgloss/v2"

// Nerd Font icons, kept identical to the Python dep-triage TUI.
const (
	iconCollapsed  = "▸"
	iconExpanded   = "▾"
	iconBranch     = "│"
	iconLast       = "└"
	iconBoxOff     = "󰄱"
	iconBoxPartial = "󰄴"
	iconBoxOn      = "󰄵"
	iconBoxBlocked = "󰡖"
	iconCheckOK    = "󰄬"
)

var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)
