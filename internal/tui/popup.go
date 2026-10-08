package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// popupWidth is the content width of popups on a w-cell terminal (border and
// padding add 4 cells).
func popupWidth(w int) int { return max(10, min(w-4, 76)) }

// popupBox frames lines, each at most popupWidth cells, as a popup.
func popupBox(lines []string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styleCyan.GetForeground()).
		Padding(0, 1).Render(strings.Join(lines, "\n"))
}

// dim renders a frame faint, as the background of a popup.
func dim(s string) string { return styleDim.Render(ansi.Strip(s)) }

// overlay draws popup centered over frame, a w×h screen. The popup is cut to
// fit, so the result is never larger than the frame (the compositor's canvas
// is the union of its layers).
func overlay(frame, popup string, w, h int) string {
	lines := strings.Split(popup, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	popup = strings.Join(lines, "\n")
	pw, ph := lipgloss.Width(popup), lipgloss.Height(popup)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(frame),
		lipgloss.NewLayer(popup).X(max(0, (w-pw)/2)).Y(max(0, (h-ph)/2)).Z(1),
	).Render()
}
