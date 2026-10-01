package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/safe"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

// row is one line of the list screen: a group header (pr == nil) or a PR,
// optionally as a child of an expanded group.
type row struct {
	group *model.Group
	pr    *model.PR
	child bool
	last  bool
}

func (r row) isGroup() bool { return r.pr == nil }

var sortModes = []string{"package", "severity", "checks", "repo"}

func nextSort(mode string) string {
	for i, m := range sortModes {
		if m == mode {
			return sortModes[(i+1)%len(sortModes)]
		}
	}
	return sortModes[0]
}

func groupKey(g *model.Group) string {
	if g.ID != "" {
		return g.ID
	}
	return g.PackageKey + "\x00" + g.TargetVersion + "\x00" + g.Bump
}

func buildRows(groups []*model.Group, sortMode string, expanded map[string]bool) []row {
	rows := []row{}
	if sortMode != "package" {
		var prs []*model.PR
		for _, g := range groups {
			prs = append(prs, g.PRs...)
		}
		for _, pr := range triage.SortPRs(prs, sortMode) {
			rows = append(rows, row{pr: pr})
		}
		return rows
	}
	for _, g := range groups {
		if len(g.PRs) == 1 {
			rows = append(rows, row{pr: g.PRs[0]})
			continue
		}
		rows = append(rows, row{group: g})
		if expanded[groupKey(g)] {
			for i, pr := range g.PRs {
				rows = append(rows, row{group: g, pr: pr, child: true, last: i == len(g.PRs)-1})
			}
		}
	}
	return rows
}

func selectable(pr *model.PR) bool { return pr.Status == model.StatusReady }

func groupSelectable(g *model.Group, jobs map[string]executor.Job) []*model.PR {
	out := []*model.PR{}
	for _, pr := range g.PRs {
		if markable(pr, jobs) {
			out = append(out, pr)
		}
	}
	return out
}

var blockerOrder = []struct{ code, badge string }{
	{model.BlockerBehindBase, "behind"},
	{model.BlockerConflicts, "conflict"},
	{model.BlockerReviewRequired, "review"},
	{model.BlockerChangesRequested, "changes"},
	{model.BlockerSuperseded, "superseded"},
	{model.BlockerStale, "stale"},
	{model.BlockerBlockedUnknown, "blocked"},
}

func checkBadge(c model.CheckSummary) string {
	switch {
	case c.Failed > 0:
		return fmt.Sprintf("F%d", c.Failed)
	case c.Pending > 0:
		return fmt.Sprintf("P%d", c.Pending)
	case c.Total > 0:
		return iconCheckOK
	default:
		return "--"
	}
}

func prBadges(pr *model.PR) []string {
	out := []string{}
	has := map[string]bool{}
	for _, b := range pr.Blockers {
		has[b.Code] = true
	}
	for _, b := range blockerOrder {
		if has[b.code] {
			out = append(out, b.badge)
		}
	}
	if pr.Status == model.StatusMerging {
		out = append(out, "merging")
	}
	scope := "review"
	if pr.ViewerApproved {
		scope = "approved"
	}
	return append(out, scope, checkBadge(pr.Checks))
}

func groupBadges(g *model.Group, sel map[string]bool, jobs map[string]executor.Job) []string {
	selected, failed, pending := 0, 0, 0
	for _, pr := range g.PRs {
		if sel[pr.Ref] {
			selected++
		}
		switch {
		case pr.Checks.Failed > 0:
			failed++
		case pr.Checks.Pending > 0:
			pending++
		}
	}
	out := []string{}
	if len(groupSelectable(g, jobs)) == 0 {
		out = append(out, "blocked")
	}
	out = append(out, fmt.Sprintf("sel %d/%d", selected, len(g.PRs)))
	switch {
	case failed > 0:
		out = append(out, fmt.Sprintf("F%d", failed))
	case pending > 0:
		out = append(out, fmt.Sprintf("P%d", pending))
	default:
		out = append(out, iconCheckOK)
	}
	return out
}

// prCheckbox shows the job icon while a job is queued or running.
func prCheckbox(pr *model.PR, sel map[string]bool, jobs map[string]executor.Job) string {
	if j, ok := jobs[refKey(pr.Ref)]; ok && !j.Finished() {
		return jobIcon(j)
	}
	switch {
	case !selectable(pr):
		return iconBoxBlocked
	case sel[pr.Ref]:
		return iconBoxOn
	default:
		return iconBoxOff
	}
}

func groupCheckbox(g *model.Group, sel map[string]bool, jobs map[string]executor.Job) string {
	ready := groupSelectable(g, jobs)
	n := 0
	for _, pr := range ready {
		if sel[pr.Ref] {
			n++
		}
	}
	switch {
	case len(ready) == 0:
		return iconBoxBlocked
	case n == len(ready):
		return iconBoxOn
	case n > 0:
		return iconBoxPartial
	default:
		return iconBoxOff
	}
}

func groupLabel(g *model.Group) string {
	parts := []string{g.Package}
	if g.TargetVersion != "" {
		parts = append(parts, "-> "+g.TargetVersion)
	}
	parts = append(parts, "["+g.Bump+"]", fmt.Sprintf("(%d PRs)", len(g.PRs)))
	return strings.Join(parts, " ")
}

func updateLabel(pr *model.PR) string {
	if pr.TargetVersion == "" {
		return pr.Title
	}
	return fmt.Sprintf("%s -> %s [%s]", pr.Package, pr.TargetVersion, pr.Bump)
}

func versionLabel(pr *model.PR) string {
	switch {
	case pr.SourceVersion != "" && pr.TargetVersion != "":
		return pr.SourceVersion + " -> " + pr.TargetVersion
	case pr.TargetVersion != "":
		return "-> " + pr.TargetVersion
	default:
		return pr.Title
	}
}

func rowParts(r row, sel, expanded map[string]bool, jobs map[string]executor.Job) (lead, box, body string, badges []string) {
	if r.isGroup() {
		lead = iconCollapsed
		if expanded[groupKey(r.group)] {
			lead = iconExpanded
		}
		return lead, groupCheckbox(r.group, sel, jobs), groupLabel(r.group), groupBadges(r.group, sel, jobs)
	}
	badges = prBadges(r.pr)
	if j, ok := jobs[refKey(r.pr.Ref)]; ok {
		badges = append([]string{jobIcon(j) + " " + jobWord(j)}, badges...)
	}
	box = prCheckbox(r.pr, sel, jobs)
	if r.child {
		lead = iconBranch
		if r.last {
			lead = iconLast
		}
		return lead, box, r.pr.Ref + "  " + versionLabel(r.pr), badges
	}
	return " ", box, r.pr.Ref + "  " + updateLabel(r.pr), badges
}

// layoutRow fits a row into exactly width cells: lead and checkbox on the
// left, badges right-aligned, the (sanitized) body truncated in between.
func layoutRow(lead, box, body string, badges []string, width int) string {
	left := lead + " " + box + " "
	right := strings.Join(badges, "  ")
	avail := max(1, width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	body = ansi.Truncate(safe.Inline(body), avail, "...")
	pad := max(1, width-lipgloss.Width(left)-lipgloss.Width(body)-lipgloss.Width(right))
	return ansi.Truncate(left+body+strings.Repeat(" ", pad)+right, width, "")
}

func rowStyle(r row, sel map[string]bool, focused bool, jobs map[string]executor.Job) lipgloss.Style {
	s := lipgloss.NewStyle()
	failed, warn := false, false
	if r.isGroup() {
		s = s.Bold(true)
		warn = len(groupSelectable(r.group, jobs)) == 0
		for _, pr := range r.group.PRs {
			failed = failed || pr.Checks.Failed > 0
			warn = warn || pr.Checks.Pending > 0
		}
	} else {
		failed = r.pr.Checks.Failed > 0
		warn = !selectable(r.pr) || r.pr.Checks.Pending > 0
		if j, ok := jobs[refKey(r.pr.Ref)]; ok && j.State == executor.JobDone && j.Result.Status == model.ResultSuccess {
			s = s.Faint(true)
		}
	}
	switch {
	case failed:
		s = s.Foreground(styleRed.GetForeground())
	case warn:
		s = s.Foreground(styleYellow.GetForeground())
	}
	if focused {
		s = s.Reverse(true)
	}
	return s
}
