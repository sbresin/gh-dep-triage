package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

type phase int

const (
	phaseRefreshing phase = iota
	phaseExecuting
	phaseDone
)

type refreshedMsg struct {
	snap *model.Snapshot
	err  error
}

type resultMsg struct{ result model.Result }

type execDoneMsg struct{}

func waitFor(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

func (m Model) findPR(ref string) *model.PR {
	for _, pr := range m.snap.PRs() {
		if pr.Ref == ref {
			return pr
		}
	}
	return nil
}

func (m Model) startConfirm() (tea.Model, tea.Cmd) {
	m.confirmPRs = nil
	for _, pr := range triage.SortPRs(m.snap.PRs(), "repo") {
		if m.selected[pr.Ref] {
			m.confirmPRs = append(m.confirmPRs, pr)
		}
	}
	rows := []listRow{}
	lastRepo := ""
	for _, pr := range m.confirmPRs {
		if pr.Repo != lastRepo {
			rows = append(rows, listRow{header: true, text: pr.Repo, style: styleCyan.Bold(true)})
			lastRepo = pr.Repo
		}
		text, style := fmt.Sprintf("#%d  approve+merge  %s", pr.Number, updateLabel(pr)), lipgloss.NewStyle()
		if v := policy.Evaluate(model.ActionMerge, pr, m.deps.Rules); !v.Allow {
			text, style = fmt.Sprintf("#%d  denied (%s)  %s", pr.Number, v.Reason, updateLabel(pr)), styleRed
		}
		rows = append(rows, listRow{text: text, ref: pr.Ref, style: style})
	}
	m.list, m.screen = newListView(rows), screenConfirm
	m.status = "o opens the focused PR. d shows the description. b shows blockers."
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if nm, cmd, handled := m.prAction(k); handled {
		return nm, cmd
	}
	switch k {
	case "q", "esc":
		m.screen, m.status = screenList, "Space toggles Approve+Merge for the focused row."
	case "j", "down":
		m.list.move(1, m.height-3)
	case "k", "up":
		m.list.move(-1, m.height-3)
	case "enter":
		return m.startExecution()
	}
	return m, nil
}

func (m Model) viewConfirm() string {
	sub := fmt.Sprintf("%d PRs selected. Enter executes. q returns to triage.", len(m.confirmPRs))
	return m.frame("Confirm planned actions", sub, m.list.render(m.width, m.height-3), m.status)
}

func (m Model) startExecution() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.runCtx, m.cancel = ctx, cancel
	m.screen, m.phase, m.cancelling = screenProgress, phaseRefreshing, false
	m.total, m.results = len(m.confirmPRs), nil
	m.logs = []string{"Refreshing PR data before executing..."}
	load := m.deps.Load
	return m, func() tea.Msg {
		s, err := load(ctx)
		return refreshedMsg{snap: s, err: err}
	}
}

func (m *Model) finish(line string) {
	m.phase = phaseDone
	if line != "" {
		m.logs = append(m.logs, line)
	}
	m.cancel()
}

func (m Model) onRefreshed(msg refreshedMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.runCtx.Err() != nil:
		m.finish("Cancelled before any change was made.")
		return m, nil
	case msg.err != nil:
		m.finish("Refresh failed: " + msg.err.Error())
		return m, nil
	}
	items := make([]plan.Item, len(m.confirmPRs))
	for i, pr := range m.confirmPRs {
		items[i] = plan.Item{Action: model.ActionMerge, Ref: pr.Ref, HeadOid: pr.HeadOid}
	}
	tasks, err := plan.Resolve(msg.snap, items)
	if err != nil {
		m.finish("Could not resolve the selection: " + err.Error())
		return m, nil
	}
	var run []plan.Task
	for _, t := range tasks {
		if t.Result != nil {
			m.addResult(*t.Result)
			continue
		}
		if v := policy.Evaluate(t.Action, t.PR, m.deps.Rules); !v.Allow {
			m.addResult(model.Result{Action: t.Action, Ref: t.PR.Ref, Status: model.ResultDenied, Reason: v.Reason,
				Message: v.Message, Steps: []string{}, HeadOid: t.PR.HeadOid})
			continue
		}
		run = append(run, t)
	}
	if len(run) == 0 {
		m.finish("Nothing left to execute.")
		return m, nil
	}
	m.phase = phaseExecuting
	m.ch = make(chan tea.Msg, len(run)+1)
	ch, ctx, execute := m.ch, m.runCtx, m.deps.Execute
	return m, func() tea.Msg {
		go func() {
			execute(ctx, run, func(r model.Result) { ch <- resultMsg{result: r} })
			ch <- execDoneMsg{}
		}()
		return <-ch
	}
}

func resultDetail(r model.Result) string {
	parts := []string{}
	if r.Reason != "" {
		parts = append(parts, r.Reason)
	}
	if len(r.Steps) > 0 {
		parts = append(parts, strings.Join(r.Steps, ", "))
	}
	if r.Message != "" {
		parts = append(parts, r.Message)
	}
	return strings.Join(parts, ": ")
}

func (m *Model) addResult(r model.Result) {
	m.results = append(m.results, r)
	m.logs = append(m.logs, fmt.Sprintf("%-8s %s  %s", r.Status, r.Ref, resultDetail(r)))
}

func (m Model) updateProgress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.phase == phaseDone {
		if k == "ctrl+c" {
			m.interrupted = true
			return m, tea.Quit
		}
		return m.showResults(), nil
	}
	if k == "ctrl+c" {
		if m.cancelling {
			m.interrupted = true
			return m, tea.Quit
		}
		m.cancelling = true
		m.cancel()
		m.logs = append(m.logs, "Cancelling: queued PRs will be skipped; in-flight requests finish.")
	}
	return m, nil
}

func (m Model) viewProgress() string {
	title := "Refreshing..."
	switch m.phase {
	case phaseExecuting:
		title = fmt.Sprintf("Executing %d/%d", len(m.results), m.total)
	case phaseDone:
		title = fmt.Sprintf("Done %d/%d", len(m.results), m.total)
	}
	visible := m.height - 3
	lines := m.logs
	if len(lines) > visible {
		lines = lines[len(lines)-visible:]
	}
	body := make([]string, len(lines))
	for i, l := range lines {
		body[i] = clip(l, m.width)
	}
	footer := "Working... ctrl+c cancels queued PRs."
	switch {
	case m.phase == phaseDone:
		footer = "Done. Press any key to see the results."
	case m.cancelling:
		footer = "Cancelling... press ctrl+c again to quit immediately."
	}
	return m.frame(title, "Approve+Merge, pinned to the commits you reviewed", body, footer)
}

func (m Model) showResults() Model {
	sections := []struct {
		title, status string
		style         lipgloss.Style
	}{
		{"Failures", model.ResultFailed, styleRed.Bold(true)},
		{"Denied", model.ResultDenied, styleRed.Bold(true)},
		{"Skipped", model.ResultSkipped, styleYellow.Bold(true)},
		{"Succeeded", model.ResultSuccess, styleGreen.Bold(true)},
	}
	rows := []listRow{}
	for _, s := range sections {
		var items []model.Result
		for _, r := range m.results {
			if r.Status == s.status {
				items = append(items, r)
			}
		}
		slices.SortStableFunc(items, func(a, b model.Result) int { return compareRefs(a.Ref, b.Ref) })
		rows = append(rows, listRow{header: true, text: fmt.Sprintf("%s (%d)", s.title, len(items)), style: s.style})
		if len(items) == 0 {
			rows = append(rows, listRow{header: true, text: "  none", style: styleDim})
			continue
		}
		for _, r := range items {
			rows = append(rows, listRow{text: r.Ref + "  " + resultDetail(r), ref: r.Ref})
		}
	}
	m.list, m.screen = newListView(rows), screenResults
	m.status = "o opens the focused PR. d shows the description. q exits."
	return m
}

// compareRefs orders owner/repo#n refs by repo, then numerically by number.
func compareRefs(a, b string) int {
	split := func(ref string) (string, int) {
		i := strings.LastIndex(ref, "#")
		if i < 0 {
			return ref, 0
		}
		n, _ := strconv.Atoi(ref[i+1:])
		return ref[:i], n
	}
	ra, na := split(a)
	rb, nb := split(b)
	if c := strings.Compare(ra, rb); c != 0 {
		return c
	}
	return cmp.Compare(na, nb)
}

func (m Model) updateResults(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if nm, cmd, handled := m.prAction(k); handled {
		return nm, cmd
	}
	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "j", "down":
		m.list.move(1, m.height-3)
	case "k", "up":
		m.list.move(-1, m.height-3)
	}
	return m, nil
}

func (m Model) viewResults() string {
	counts := map[string]int{}
	for _, r := range m.results {
		counts[r.Status]++
	}
	sub := fmt.Sprintf("%d succeeded, %d skipped, %d denied, %d failed",
		counts[model.ResultSuccess], counts[model.ResultSkipped], counts[model.ResultDenied], counts[model.ResultFailed])
	return m.frame("Execution results", sub, m.list.render(m.width, m.height-3), m.status)
}
