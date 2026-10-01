package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

type browsedMsg struct {
	ref string
	err error
}

var noPRMessage = map[string]string{
	"o": "Open works on PR rows only.",
	"d": "Description view works on PR rows only.",
	"b": "Blocker details work on PR rows only.",
}

// focusedPR is the PR under the cursor on the current screen, or nil.
func (m Model) focusedPR() *model.PR {
	if m.screen == screenList {
		rows := m.rows()
		if m.cursor < len(rows) {
			return rows[m.cursor].pr
		}
	}
	return nil
}

// prAction handles o, d and b for the focused PR. handled is false for other keys.
func (m Model) prAction(k string) (Model, tea.Cmd, bool) {
	if _, ok := noPRMessage[k]; !ok {
		return m, nil, false
	}
	pr := m.focusedPR()
	if pr == nil {
		m.status = noPRMessage[k]
		return m, nil, true
	}
	switch k {
	case "o":
		browse, ref, url := m.deps.Browse, pr.Ref, pr.URL
		return m, func() tea.Msg { return browsedMsg{ref: ref, err: browse(url)} }, true
	case "d":
		return m.openPager("Description: "+pr.Ref, describe(pr, m.width)), nil, true
	default:
		return m.openPager("Blockers: "+pr.Ref, blockerText(pr)), nil, true
	}
}

func (m Model) openPager(title, content string) Model {
	m.pager = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(max(1, m.height-3)))
	m.pager.SetContent(content)
	m.pagerTitle, m.pagerReturn, m.screen = title, m.screen, screenPager
	return m
}

func (m Model) updatePager(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k := msg.String(); k == "q" || k == "esc" {
		m.screen = m.pagerReturn
		return m, nil
	}
	var cmd tea.Cmd
	m.pager, cmd = m.pager.Update(msg)
	return m, cmd
}

func (m Model) viewPager() string {
	return m.frame(m.pagerTitle, "j/k or arrows scroll  q back", strings.Split(m.pager.View(), "\n"), "")
}

// describe renders the PR description as Markdown. Untrusted text is
// sanitized before rendering.
func describe(pr *model.PR, width int) string {
	body := strings.TrimSpace(safe.Text(pr.Body))
	if body == "" {
		body = "_No description provided._"
	}
	md := fmt.Sprintf("# %s\n\n%s\n\n%s\n\n---\n\n%s\n", safe.Line(pr.Title), pr.Ref, pr.URL, body)
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, width-2)))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

func blockerText(pr *model.PR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s\n\n", pr.Ref, updateLabel(pr), pr.Status)
	if len(pr.Blockers) == 0 {
		b.WriteString("No blockers.")
		if pr.Status == model.StatusMerging {
			b.WriteString(" Auto-merge is enabled; GitHub merges it once required checks pass.")
		}
		b.WriteString("\n")
	}
	for _, bl := range pr.Blockers {
		fmt.Fprintf(&b, "- %s: %s\n", bl.Code, bl.Detail)
		for _, a := range bl.SuggestedActions {
			cmd := a.Command
			if cmd == "" {
				cmd = "(no command)"
			}
			fmt.Fprintf(&b, "    %s: %s\n", a.Action, cmd)
			if a.Checkout != "" {
				fmt.Fprintf(&b, "    checkout: %s\n", a.Checkout)
			}
		}
	}
	if pr.Checks.Pending > 0 {
		fmt.Fprintf(&b, "\nPending checks: %s\n", strings.Join(pr.Checks.PendingNames, ", "))
	}
	return safe.Text(b.String())
}
