package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

const pinnedNote = "Pinned to the commits shown. Each PR is re-checked right before it runs."

// confirmState is the open confirm popup.
type confirmState struct {
	prs    []*model.PR       // sorted by repo, then number
	denied map[string]string // ref → hard-rule reason; these are not queued
	moved  bool              // a retry whose head moved since the failed attempt
	scroll int
}

// markedPRs are the marked PRs that can still be queued.
func (m Model) markedPRs() []*model.PR {
	out := []*model.PR{}
	for _, pr := range m.snap.PRs() {
		if m.selected[pr.Ref] && selectable(pr) {
			out = append(out, pr)
		}
	}
	return out
}

// openConfirm opens the confirm popup for prs; retryOf is the finished job
// being retried, or nil.
func (m Model) openConfirm(prs []*model.PR, retryOf *executor.Job) Model {
	c := confirmState{prs: triage.SortPRs(prs, "repo"), denied: map[string]string{}}
	for _, pr := range c.prs {
		if v := policy.Evaluate(model.ActionMerge, pr, m.deps.Rules); !v.Allow {
			c.denied[pr.Ref] = v.Reason
		}
	}
	if retryOf != nil && len(c.prs) == 1 {
		c.moved = retryOf.PR.HeadOid != c.prs[0].HeadOid
	}
	m.confirm, m.popup = c, popupConfirm
	return m
}

// confirmRows is how many PR lines the popup shows before it scrolls.
func (m Model) confirmRows() int { return max(1, m.height-10) }

func (m Model) updateConfirm(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "y":
		return m.submitConfirmed()
	case "n", "esc", "q":
		m.popup, m.status = popupNone, "Nothing queued."
	case "j", "down":
		m.confirm.scroll = min(m.confirm.scroll+1, max(0, len(m.confirm.prs)-m.confirmRows()))
	case "k", "up":
		m.confirm.scroll = max(0, m.confirm.scroll-1)
	}
	return m, nil
}

func (m Model) submitConfirmed() (tea.Model, tea.Cmd) {
	m.popup = popupNone
	allowed := []*model.PR{}
	for _, pr := range m.confirm.prs {
		if _, denied := m.confirm.denied[pr.Ref]; !denied {
			allowed = append(allowed, pr)
		}
	}
	if len(allowed) == 0 {
		m.status = "Nothing to queue: every PR fails a hard rule."
		return m, nil
	}
	if m.deps.Queue == nil {
		m.status = "No work queue is available."
		return m, nil
	}
	queued, problems := 0, []string{}
	for _, pr := range allowed {
		if _, err := m.deps.Queue.Submit(model.ActionMerge, pr); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", pr.Ref, err))
			continue
		}
		queued++
	}
	m.selected = map[string]bool{}
	m.syncJobs(false)
	m.fixScroll()
	m.status = fmt.Sprintf("Queued %d PR(s) for Approve+Merge.", queued)
	if len(problems) > 0 {
		m.status += " Not queued: " + strings.Join(problems, "; ") + "."
	}
	return m, nil
}

func (m Model) viewConfirm() string {
	c, w := m.confirm, popupWidth(m.width)
	lines := []string{styleBold.Render(clip(fmt.Sprintf("Approve + Merge %d PRs?", len(c.prs)), w)), ""}
	end := min(len(c.prs), c.scroll+m.confirmRows())
	for _, pr := range c.prs[c.scroll:end] {
		mark := "  "
		if pr.Bump == model.BumpMajor {
			mark = "⚠ "
		}
		text, style := fmt.Sprintf("%s%s  #%d  %s", mark, pr.Repo, pr.Number, updateLabel(pr)), lipgloss.NewStyle()
		if reason, denied := c.denied[pr.Ref]; denied {
			text, style = text+"  denied ("+reason+")", styleRed
		}
		lines = append(lines, style.Render(clip(text, w)))
	}
	if len(c.prs) > m.confirmRows() {
		lines = append(lines, styleDim.Render(fmt.Sprintf("%d-%d of %d, j/k scroll", c.scroll+1, end, len(c.prs))))
	}
	lines = append(lines, "")
	if c.moved {
		lines = append(lines, styleYellow.Render(clip("new commit since the last attempt", w)))
	}
	lines = append(lines, clip(pinnedNote, w), "", styleDim.Render("y queue  n/esc back"))
	return popupBox(lines)
}
