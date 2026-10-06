package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

type browsedMsg struct {
	ref string
	err error
}

var noPRMessage = map[string]string{
	"o": "Open works on PR rows only.",
	"d": "Details work on PR rows only.",
	"b": "Details work on PR rows only.",
}

// detailsState is the open PR details popup: blockers, deps.dev risk and the
// description in one scrollable view.
type detailsState struct {
	pr       *model.PR
	job      *executor.Job // set when opened from the queue pane
	actions  bool          // r/R/x act on the PR (opened from the list)
	content  string
	descLine int // first line of the Description section
	vp       viewport.Model
}

var blockerTitles = map[string]string{
	model.BlockerChecksFailing:    "Checks failing",
	model.BlockerBehindBase:       "Behind base branch",
	model.BlockerConflicts:        "Merge conflicts",
	model.BlockerReviewRequired:   "Needs another approval",
	model.BlockerChangesRequested: "Changes requested",
	model.BlockerSuperseded:       "Superseded by a newer PR",
	model.BlockerStale:            "Stale",
	model.BlockerBlockedUnknown:   "Blocked for an unknown reason",
}

var actionLabels = map[string]string{
	model.ActionRebase:        "rebase",
	model.ActionRecreate:      "recreate",
	model.ActionRerun:         "re-run failed checks",
	model.ActionClose:         "close",
	model.ActionRequestReview: "request a review",
	"fix":                     "fix",
}

// focusedPR is the PR under the cursor of the focused pane, or nil.
func (m Model) focusedPR() *model.PR {
	if m.focus == paneQueue {
		if j, ok := m.focusedJob(); ok {
			return j.PR
		}
		return nil
	}
	rows := m.rows()
	if m.cursor < len(rows) {
		return rows[m.cursor].pr
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
	if k == "o" {
		browse, ref, url := m.deps.Browse, pr.Ref, pr.URL
		return m, func() tea.Msg { return browsedMsg{ref: ref, err: browse(url)} }, true
	}
	var job *executor.Job
	if j, ok := m.focusedJob(); ok && m.focus == paneQueue {
		job = &j
	}
	return m.openDetails(pr, job, job == nil, k == "d"), nil, true
}

// detailsWidth is the popup's content width; border and padding add 4.
func (m Model) detailsWidth() int { return max(20, min(m.width-6, 100)) }

// detailsHeight is the viewport height: the screen minus a margin, the
// border, the header with its blank line and the footer.
func (m Model) detailsHeight() int { return max(1, m.height-7) }

func (m Model) openDetails(pr *model.PR, job *executor.Job, actions, atDescription bool) Model {
	content, desc := detailsContent(pr, job, actions, m.detailsWidth())
	vp := viewport.New(viewport.WithWidth(m.detailsWidth()), viewport.WithHeight(m.detailsHeight()))
	vp.SetContent(content)
	if atDescription {
		vp.SetYOffset(desc)
	}
	m.details = detailsState{pr: pr, job: job, actions: actions, content: content, descLine: desc, vp: vp}
	m.popup = popupDetails
	return m
}

// resizeDetails re-renders the open popup for a new terminal size.
func (m Model) resizeDetails() Model {
	d := m.details
	m = m.openDetails(d.pr, d.job, d.actions, false)
	m.details.vp.SetYOffset(d.vp.YOffset())
	return m
}

func (m Model) updateDetails(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "esc", "q":
		m.popup = popupNone
		return m, nil
	case "b":
		m.details.vp.GotoTop()
		return m, nil
	case "d":
		m.details.vp.SetYOffset(m.details.descLine)
		return m, nil
	case "o":
		browse, ref, url := m.deps.Browse, m.details.pr.Ref, m.details.pr.URL
		return m, func() tea.Msg { return browsedMsg{ref: ref, err: browse(url)} }
	case "r", "R", "x":
		if !m.details.actions {
			return m, nil
		}
		m.popup = popupNone
		return m.unblock(k, row{pr: m.details.pr})
	}
	var cmd tea.Cmd
	m.details.vp, cmd = m.details.vp.Update(msg)
	return m, cmd
}

// detailsKeys lists the footer keys; action keys only where they apply.
func (d detailsState) keys() string {
	var keys []string
	if d.actions {
		keys = append(keys, "r rebase")
		if d.pr.Checks.Failed > 0 {
			keys = append(keys, "R rerun")
		}
		if d.pr.HasBlocker(model.BlockerSuperseded) {
			keys = append(keys, "x close")
		}
	}
	return strings.Join(append(keys, "b/d jump", "j/k scroll", "o open", "esc close"), "  ")
}

func (m Model) viewDetails() string {
	d, w := m.details, m.detailsWidth()
	pr := d.pr
	status := styleYellow.Render(string(pr.Status))
	if pr.Status == model.StatusReady {
		status = string(pr.Status)
	}
	head := ansi.Truncate(styleBold.Render(safe.Inline(pr.Ref))+"  "+safe.Inline(detailsLabel(pr)), max(1, w-lipgloss.Width(status)-2), "...")
	pct := fmt.Sprintf("%3.0f%%", d.vp.ScrollPercent()*100)
	keys := ansi.Truncate(d.keys(), max(1, w-lipgloss.Width(pct)-2), "...")
	lines := []string{spread(head, status, w), ""}
	lines = append(lines, strings.Split(d.vp.View(), "\n")...)
	return popupBox(append(lines, styleDim.Render(spread(keys, pct, w))))
}

// spread puts left and right on one line of width cells.
func spread(left, right string, width int) string {
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func detailsLabel(pr *model.PR) string {
	if pr.TargetVersion == "" {
		return pr.Title
	}
	return fmt.Sprintf("%s %s [%s]", pr.Package, versionLabel(pr), pr.Bump)
}

// detailsContent renders the scrollable part of the popup and returns the
// line where the Description section starts. All PR, job and deps.dev text
// is untrusted and sanitized before it is styled.
func detailsContent(pr *model.PR, job *executor.Job, actions bool, width int) (string, int) {
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }
	// wrap adds s wrapped to the width after indent, each line styled.
	wrap := func(indent string, style lipgloss.Style, s string) {
		for _, l := range strings.Split(ansi.Wrap(safe.Text(s), max(10, width-len(indent)), ""), "\n") {
			add(indent + style.Render(l))
		}
	}
	plainStyle := lipgloss.NewStyle()

	if job != nil {
		add(styleBold.Render("Job"))
		wrap("  ", plainStyle, job.Action+"  "+jobWord(*job))
		if job.State == executor.JobRunning && job.Step != "" {
			wrap("  ", plainStyle, "step: "+job.Step)
		}
		if job.Finished() {
			r := job.Result
			wrap("  ", plainStyle, "status: "+r.Status)
			if r.Reason != "" {
				wrap("  ", plainStyle, "reason: "+r.Reason)
			}
			if r.Message != "" {
				wrap("  ", plainStyle, "message: "+r.Message)
			}
			if len(r.Steps) > 0 {
				wrap("  ", plainStyle, "steps: "+strings.Join(r.Steps, ", "))
			}
		}
		add("")
	}

	add(styleBold.Render("Blockers"))
	if len(pr.Blockers) == 0 {
		note := "No blockers."
		if pr.Status == model.StatusMerging {
			note += " Auto-merge is enabled; GitHub merges it once required checks pass."
		}
		wrap("  ", plainStyle, note)
	}
	for _, bl := range pr.Blockers {
		title, ok := blockerTitles[bl.Code]
		if !ok {
			title = bl.Code
		}
		add(spread(styleRed.Render("✗ "+title), styleDim.Render(bl.Code), width))
		wrap("  ", plainStyle, bl.Detail)
		for _, a := range bl.SuggestedActions {
			label := actionLabels[a.Action]
			if label == "" {
				label = safe.Inline(a.Action)
			}
			if k := actionKey(a); actions && k != "" {
				add("  " + styleCyan.Render("→ "+k) + "  " + label)
				continue
			}
			add("  " + styleCyan.Render("→ ") + label + ":")
			if a.Command != "" {
				wrap("      ", styleDim, a.Command)
			}
			if a.Checkout != "" {
				wrap("      ", styleDim, a.Checkout)
			}
		}
		add("")
	}
	if pr.Checks.Pending > 0 {
		wrap("", styleYellow, "⏳ Pending: "+strings.Join(pr.Checks.PendingNames, ", "))
		add("")
	}

	add(styleBold.Render("Dependency (deps.dev)"))
	add(riskLines(pr.Risk)...)
	add("")

	desc := len(lines)
	add(styleBold.Render("Description"))
	add(strings.Split(strings.TrimRight(describe(pr, width), "\n"), "\n")...)

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n"), desc
}

// actionKey is the list key that queues a suggested action, or "".
func actionKey(a model.SuggestedAction) string {
	switch {
	case a.Action == model.ActionRebase:
		return "r"
	case a.Action == model.ActionRerun:
		return "R"
	case a.Action == model.ActionClose && strings.Contains(a.Command, "--reason "+model.BlockerSuperseded):
		return "x"
	}
	return ""
}

func riskLines(r *model.Risk) []string {
	if r == nil {
		return []string{"  No deps.dev data (ecosystem not covered, or the lookup failed)."}
	}
	var out []string
	var project []string
	if r.SourceRepo != "" {
		project = append(project, safe.Inline(r.SourceRepo))
	}
	if r.Stars > 0 {
		project = append(project, commas(r.Stars)+" ★")
	}
	if r.Scorecard > 0 {
		project = append(project, fmt.Sprintf("scorecard %.1f", r.Scorecard))
	}
	if len(project) > 0 {
		out = append(out, "  "+strings.Join(project, "   "))
	}
	var facts []string
	if !r.PublishedAt.IsZero() {
		facts = append(facts, "published "+r.PublishedAt.Format("2006-01-02"))
	}
	if r.Deprecated {
		facts = append(facts, styleYellow.Render("deprecated"))
	}
	if len(facts) > 0 {
		out = append(out, "  "+strings.Join(facts, "   "))
	}
	for _, f := range r.Findings {
		text, style := safe.Inline(f), styleYellow
		switch f {
		case model.FindingMalicious:
			style = styleRed.Bold(true)
		case model.FindingCooldown:
			if !r.CooldownEnd.IsZero() {
				text += " until " + r.CooldownEnd.UTC().Format("2006-01-02 15:04 MST")
			}
		}
		out = append(out, "  "+style.Render(text))
	}
	if len(r.Advisories) > 0 {
		out = append(out, "  "+styleYellow.Render("advisories: "+safe.Inline(strings.Join(r.Advisories, ", "))))
	}
	if len(out) == 0 {
		out = append(out, "  deps.dev knows nothing more about this version.")
	}
	return out
}

// commas formats n with thousands separators: 108000 → "108,000".
func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// describe renders the PR description as Markdown. Untrusted text is
// sanitized before rendering.
func describe(pr *model.PR, width int) string {
	body := strings.TrimSpace(safe.Text(pr.Body))
	if body == "" {
		body = "_No description provided._"
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, width-4)))
	if err != nil {
		return body
	}
	out, err := r.Render(body)
	if err != nil {
		return body
	}
	return out
}
