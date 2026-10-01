// Package tui implements the interactive gh dep-triage terminal UI.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

// Deps is everything the TUI needs from the outside world.
type Deps struct {
	// Load fetches a fresh snapshot (reload).
	Load func(ctx context.Context) (*model.Snapshot, error)
	// Queue runs confirmed Approve+Merge jobs in the background.
	Queue Queue
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
)

type popupKind int

const (
	popupNone popupKind = iota
	popupConfirm
	popupQuit
)

// Model is the Bubble Tea model for the TUI.
type Model struct {
	ctx  context.Context
	deps Deps
	snap *model.Snapshot

	width, height int
	screen        screen
	popup         popupKind
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

	confirm confirmState

	focus   pane
	qcursor int
	qscroll int
	qfollow bool // the queue cursor follows the newest job
	paused  bool

	quitWhenIdle bool // w in the quit popup
	cancelling   bool // Close(true) requested; quit on the Closed event
	presses      int  // ctrl+c presses and signals
	forced       bool // quit while jobs were still running

	jobs    []executor.Job          // the queue's jobs, in submission order
	badges  map[string]executor.Job // latest job per lower-cased ref
	history []executor.Job          // finished jobs, from Finished events
}

type reloadedMsg struct {
	snap *model.Snapshot
	err  error
}

func New(ctx context.Context, snap *model.Snapshot, deps Deps) Model {
	m := Model{
		ctx: ctx, deps: deps, snap: snap, width: 100, height: 30, screen: screenList,
		sortMode: "package", expanded: map[string]bool{}, selected: map[string]bool{},
		badges: map[string]executor.Job{}, qfollow: true,
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

func (m Model) Init() tea.Cmd {
	if m.deps.Queue == nil {
		return nil
	}
	return listen(m.deps.Queue.Events())
}

func (m Model) tooSmall() bool { return m.width < 40 || m.height < 6 }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		m.fixQueueScroll()
		m.pager.SetWidth(msg.Width)
		m.pager.SetHeight(max(1, msg.Height-3))
		return m, nil
	case browsedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Browser open failed for %s: %v", msg.ref, msg.err)
		} else {
			m.status = "Opened " + msg.ref + " in the browser."
		}
		// The launched browser may have written to the terminal behind our back.
		return m, tea.ClearScreen
	case queueMsg:
		return m.onQueue(msg)
	case reloadedMsg:
		return m.onReloaded(msg)
	case signalMsg:
		return m.interrupt(true)
	case ctxDoneMsg:
		return m.interrupt(false)
	case tea.KeyPressMsg:
		k := msg.String()
		if k == "ctrl+c" {
			return m.interrupt(true)
		}
		if m.tooSmall() {
			if q, r := m.pending(); (k == "q" || k == "esc") && q+r == 0 {
				return m, tea.Quit
			}
			return m, nil
		}
		switch m.popup {
		case popupConfirm:
			return m.updateConfirm(k)
		case popupQuit:
			return m.updateQuit(k)
		}
		switch m.screen {
		case screenList:
			return m.updateMain(msg)
		case screenPager:
			return m.updatePager(msg)
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
	switch {
	case m.tooSmall():
		content = styleRed.Render("Terminal too small for dep-triage.")
	case m.screen == screenPager:
		content = m.viewPager()
	default:
		content = m.viewMain()
		if m.popup == popupConfirm {
			content = overlay(dim(content), m.viewConfirm(), m.width, m.height)
		}
		if m.popup == popupQuit {
			content = overlay(dim(content), m.viewQuit(), m.width, m.height)
		}
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) updateMain(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k := msg.String(); m.quitWhenIdle && (k == "w" || k == "esc") {
		m.quitWhenIdle = false
		m.status = "Staying; the queue keeps running."
		return m, nil
	}
	if msg.String() == "tab" {
		if m.focus == paneList {
			m.focus = paneQueue
		} else {
			m.focus = paneList
		}
		return m, nil
	}
	if m.focus == paneQueue {
		return m.updateQueue(msg.String())
	}
	return m.updateList(msg)
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
