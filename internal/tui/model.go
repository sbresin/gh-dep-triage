// Package tui implements the interactive gh dep-triage terminal UI.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

// Deps is everything the TUI needs from the outside world.
type Deps struct {
	// Load fetches a fresh snapshot (reload, and right before executing).
	Load func(ctx context.Context) (*model.Snapshot, error)
	// Execute runs allowed tasks; onResult is called once per result and
	// must not block.
	Execute func(ctx context.Context, tasks []plan.Task, onResult func(model.Result)) []model.Result
	// Browse opens a URL in the user's browser.
	Browse func(url string) error
	// Rules are the policy rules; the TUI uses hard rules only.
	Rules policy.Rules
	// Who is shown in the header, e.g. "user octocat" or "team platform".
	Who string
}

type screen int

const (
	screenList screen = iota
	screenPager
	screenConfirm
	screenProgress
	screenResults
)

const listKeys = "j/k move  s sort  enter fold  space select  o open  d desc  b blockers  g reload  c confirm  q quit"

// Model is the Bubble Tea model for all TUI screens.
type Model struct {
	ctx  context.Context
	deps Deps
	snap *model.Snapshot

	width, height int
	screen        screen
	status        string
	interrupted   bool
	reloading     bool

	sortMode string
	expanded map[string]bool
	selected map[string]bool
	cursor   int
	scroll   int

	pager       viewport.Model
	pagerTitle  string
	pagerReturn screen

	confirmPRs []*model.PR
	list       listView

	runCtx     context.Context
	cancel     context.CancelFunc
	ch         chan tea.Msg
	phase      phase
	cancelling bool
	logs       []string
	results    []model.Result
	total      int
}

type reloadedMsg struct {
	snap *model.Snapshot
	err  error
}

func New(ctx context.Context, snap *model.Snapshot, deps Deps) Model {
	m := Model{
		ctx: ctx, deps: deps, snap: snap, width: 100, height: 30, screen: screenList,
		sortMode: "package", expanded: map[string]bool{}, selected: map[string]bool{},
		status: "Space toggles Approve+Merge for the focused row.",
	}
	if n := len(snap.Warnings); n > 0 {
		w := snap.Warnings[0]
		m.status = fmt.Sprintf("%d warning(s) while loading; first: %s %s", n, w.Ref, w.Message)
	}
	return m
}

// Interrupted reports whether the user quit with ctrl+c.
func (m Model) Interrupted() bool { return m.interrupted }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) tooSmall() bool { return m.width < 40 || m.height < 6 }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		m.pager.SetWidth(msg.Width)
		m.pager.SetHeight(max(1, msg.Height-3))
		return m, nil
	case browsedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Browser open failed for %s: %v", msg.ref, msg.err)
		} else {
			m.status = "Opened " + msg.ref + " in the browser."
		}
		return m, nil
	case refreshedMsg:
		return m.onRefreshed(msg)
	case resultMsg:
		m.addResult(msg.result)
		return m, waitFor(m.ch)
	case execDoneMsg:
		m.finish("")
		return m, nil
	case reloadedMsg:
		m.reloading = false
		status := ""
		if msg.err != nil {
			status = "Reload failed: " + msg.err.Error()
		} else {
			m.snap = msg.snap
			for ref := range m.selected {
				if pr := m.findPR(ref); pr == nil || !selectable(pr) {
					delete(m.selected, ref)
				}
			}
			m.fixScroll()
			status = fmt.Sprintf("Reloaded %d PRs.", len(m.snap.PRs()))
			if n := len(m.snap.Warnings); n > 0 {
				status += fmt.Sprintf(" %d warning(s).", n)
			}
		}
		if m.screen == screenList {
			m.status = status
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" && m.screen != screenProgress {
			m.interrupted = true
			return m, tea.Quit
		}
		if m.tooSmall() {
			if m.screen == screenProgress && msg.String() == "ctrl+c" {
				return m.updateProgress(msg)
			}
			if k := msg.String(); k == "q" || k == "esc" {
				return m, tea.Quit
			}
			return m, nil
		}
		switch m.screen {
		case screenList:
			return m.updateList(msg)
		case screenPager:
			return m.updatePager(msg)
		case screenConfirm:
			return m.updateConfirm(msg)
		case screenProgress:
			return m.updateProgress(msg)
		case screenResults:
			return m.updateResults(msg)
		}
	}
	if m.screen == screenPager {
		var cmd tea.Cmd
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() tea.View {
	content := ""
	if m.tooSmall() {
		content = styleRed.Render("Terminal too small for dep-triage.")
	} else {
		switch m.screen {
		case screenList:
			content = m.viewList()
		case screenPager:
			content = m.viewPager()
		case screenConfirm:
			content = m.viewConfirm()
		case screenProgress:
			content = m.viewProgress()
		case screenResults:
			content = m.viewResults()
		}
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// frame lays out a full screen: title, subtitle, body lines padded to the
// terminal height, and a status line at the bottom.
func (m Model) frame(title, subtitle string, body []string, status string) string {
	lines := []string{styleBold.Render(clip(title, m.width)), styleDim.Render(clip(subtitle, m.width))}
	lines = append(lines, body...)
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = lines[:m.height-1]
	return strings.Join(append(lines, styleDim.Render(clip(status, m.width))), "\n")
}

func clip(s string, width int) string { return ansi.Truncate(safe.Inline(s), width, "...") }
