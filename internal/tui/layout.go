package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/executor"
)

type pane int

const (
	paneList pane = iota
	paneQueue
)

const (
	listKeys  = "tab queue  pgup/dn  spc mark  c confirm  enter fold  s sort  o open  d desc  b why  g reload  q quit"
	queueKeys = "tab list  j/k move  x cancel  r retry  C clear  p pause  o open  d desc  b details  q quit"
)

func (m Model) listWidth() int  { return int(math.Round(0.65 * float64(m.width))) }
func (m Model) queueWidth() int { return m.width - m.listWidth() }

// bodyHeight is the number of rows inside each pane's border: the screen
// minus the header, the two border lines, the key help and the status line.
func (m Model) bodyHeight() int { return max(1, m.height-5) }

// box draws a pane of exactly width cells with title in its top border.
// Body lines are cut or padded to the inner width.
func box(title string, body []string, width int, focused bool) []string {
	border := styleDim
	if focused {
		border = styleCyan.Bold(true)
	}
	inner := max(0, width-2)
	label := ansi.Truncate(" "+title+" ", max(0, inner-1), "")
	lines := []string{border.Render("┌─" + label + strings.Repeat("─", max(0, inner-1-lipgloss.Width(label))) + "┐")}
	for _, l := range body {
		l = ansi.Truncate(l, inner, "")
		lines = append(lines, border.Render("│")+l+strings.Repeat(" ", max(0, inner-lipgloss.Width(l)))+border.Render("│"))
	}
	return append(lines, border.Render("└"+strings.Repeat("─", inner)+"┘"))
}

// queueCounts summarizes jobs as e.g. "✓1 ⟳1 ⏳2 ✗1"; zero counts are left out.
func queueCounts(jobs []executor.Job) string {
	n := map[string]int{}
	for _, j := range jobs {
		n[jobIcon(j)]++
	}
	parts := []string{}
	for _, icon := range []string{iconDone, iconRunning, iconQueued, iconFailed, iconSkipped} {
		if n[icon] > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", icon, n[icon]))
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) header() string {
	parts := []string{"dep-triage", "sort " + m.sortMode, fmt.Sprintf("%d marked", m.selectedCount())}
	if c := queueCounts(m.jobs); c != "" {
		parts = append(parts, "queue "+c)
	}
	if m.paused {
		parts = append(parts, "paused")
	}
	switch {
	case m.cancelling:
		parts = append(parts, "cancelling…")
	case m.quitWhenIdle:
		parts = append(parts, "quitting when idle")
	}
	return strings.Join(append(parts, m.deps.Who), " | ")
}

func (m Model) queueTitle() string {
	t := fmt.Sprintf("Queue %d", len(m.jobs))
	if c := queueCounts(m.jobs); c != "" {
		t += " · " + c
	}
	if m.paused {
		t += " · paused"
	}
	return t
}

func (m Model) listBody(width, height int) []string {
	rows := m.rows()
	if len(rows) == 0 && len(m.snap.PRs()) > 0 {
		return []string{styleDim.Render(clip("All PRs are in the queue.", width))}
	}
	out := []string{}
	for i := m.scroll; i < min(len(rows), m.scroll+height); i++ {
		lead, check, text, badges := rowParts(rows[i], m.selected, m.expanded)
		focused := m.focus == paneList && i == m.cursor
		out = append(out, rowStyle(rows[i], m.selected, focused).Render(layoutRow(lead, check, text, badges, width)))
	}
	return out
}

// viewMain is the split screen: header, list | queue, key help, status.
func (m Model) viewMain() string {
	lw, qw, h := m.listWidth(), m.queueWidth(), m.bodyHeight()
	fill := func(body []string) []string {
		for len(body) < h {
			body = append(body, "")
		}
		return body[:h]
	}
	left := box("PRs", fill(m.listBody(lw-2, h)), lw, m.focus == paneList)
	right := box(m.queueTitle(), fill(m.queueBody(qw-2, h)), qw, m.focus == paneQueue)
	keys := listKeys
	if m.focus == paneQueue {
		keys = queueKeys
	}
	lines := []string{styleBold.Render(clip(m.header(), m.width))}
	for i := range left {
		lines = append(lines, left[i]+right[i])
	}
	lines = append(lines, styleDim.Render(clip(keys, m.width)), styleDim.Render(clip(m.status, m.width)))
	return strings.Join(lines, "\n")
}
