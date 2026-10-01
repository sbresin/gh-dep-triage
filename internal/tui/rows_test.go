package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func describeRows(rows []row) []string {
	out := []string{}
	for _, r := range rows {
		switch {
		case r.isGroup():
			out = append(out, "G "+groupLabel(r.group))
		case r.child:
			out = append(out, "  "+r.pr.Ref)
		default:
			out = append(out, "P "+r.pr.Ref)
		}
	}
	return out
}

func TestBuildRowsPackageMode(t *testing.T) {
	s := fixture()
	collapsed := describeRows(buildRows(s.Groups, "package", map[string]bool{}))
	want := []string{"P acme/api#3", "P acme/api#4", "G lodash -> 4.17.21 [patch] (2 PRs)"}
	if diff := cmp.Diff(want, collapsed); diff != "" {
		t.Errorf("collapsed (-want +got):\n%s", diff)
	}
	expanded := describeRows(buildRows(s.Groups, "package", map[string]bool{"group:lodash@4.17.21": true}))
	want = append(want, "  acme/api#1", "  acme/web#2")
	if diff := cmp.Diff(want, expanded); diff != "" {
		t.Errorf("expanded (-want +got):\n%s", diff)
	}
	rows := buildRows(s.Groups, "package", map[string]bool{"group:lodash@4.17.21": true})
	if rows[3].last || !rows[4].last {
		t.Error("only the final child is marked last")
	}
}

func TestBuildRowsFlatModes(t *testing.T) {
	got := describeRows(buildRows(fixture().Groups, "repo", map[string]bool{}))
	want := []string{"P acme/api#1", "P acme/api#3", "P acme/api#4", "P acme/web#2"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestNextSort(t *testing.T) {
	mode, seen := "package", []string{}
	for range 4 {
		mode = nextSort(mode)
		seen = append(seen, mode)
	}
	if diff := cmp.Diff([]string{"severity", "checks", "repo", "package"}, seen); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestBadgesAndCheckboxes(t *testing.T) {
	s := fixture()
	find := func(ref string) *model.PR {
		for _, p := range s.PRs() {
			if p.Ref == ref {
				return p
			}
		}
		t.Fatalf("no %s", ref)
		return nil
	}
	sel := map[string]bool{"acme/api#1": true}
	if diff := cmp.Diff([]string{"behind", "review", "--"}, prBadges(find("acme/api#3"))); diff != "" {
		t.Errorf("behind PR badges (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"merging", "approved", "--"}, prBadges(find("acme/api#4"))); diff != "" {
		t.Errorf("merging PR badges (-want +got):\n%s", diff)
	}
	if prCheckbox(find("acme/api#3"), sel, nil) != iconBoxBlocked || prCheckbox(find("acme/api#4"), sel, nil) != iconBoxBlocked {
		t.Error("blocked and merging PRs show the blocked checkbox")
	}
	if prCheckbox(find("acme/api#1"), sel, nil) != iconBoxOn || prCheckbox(find("acme/web#2"), sel, nil) != iconBoxOff {
		t.Error("ready PR checkboxes follow selection")
	}
	var lodash *model.Group
	for _, g := range s.Groups {
		if g.ID == "group:lodash@4.17.21" {
			lodash = g
		}
	}
	if groupCheckbox(lodash, sel, nil) != iconBoxPartial {
		t.Error("half-selected group shows partial")
	}
	if diff := cmp.Diff([]string{"sel 1/2", iconCheckOK}, groupBadges(lodash, sel, nil)); diff != "" {
		t.Errorf("group badges (-want +got):\n%s", diff)
	}
	failing := mkPR("acme/x", 9, "Bump a from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.CheckRuns = []model.Check{{Name: "t", Kind: model.CheckKindRun, Status: "COMPLETED", Conclusion: "FAILURE"}}
	})
	if got := checkBadge(failing.Checks); got != "F1" {
		t.Errorf("check badge = %q", got)
	}
}

func TestLabels(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21")
	if got := updateLabel(pr); got != "lodash -> 4.17.21 [patch]" {
		t.Errorf("updateLabel = %q", got)
	}
	if got := versionLabel(pr); got != "4.17.20 -> 4.17.21" {
		t.Errorf("versionLabel = %q", got)
	}
	odd := mkPR("acme/api", 2, "Bump the npm group with 3 updates")
	if got := updateLabel(odd); got != "Bump the npm group with 3 updates" {
		t.Errorf("fallback updateLabel = %q", got)
	}
}

func TestLayoutRowFitsWidth(t *testing.T) {
	out := layoutRow(iconCollapsed, iconBoxOff, strings.Repeat("x", 200), []string{"sel 0/2", "F1"}, 60)
	if w := ansi.StringWidth(out); w != 60 {
		t.Errorf("width = %d, want 60: %q", w, out)
	}
	if !strings.HasSuffix(out, "sel 0/2  F1") || !strings.Contains(out, "...") {
		t.Errorf("badges right-aligned, body truncated: %q", out)
	}
	if w := ansi.StringWidth(layoutRow(" ", iconBoxOff, "short", []string{"review", "--"}, 40)); w != 40 {
		t.Errorf("short row padded to 40, got %d", w)
	}
}

func TestRowsStripControlSequences(t *testing.T) {
	evil := mkPR("acme/api", 7, "Bump \x1b]52;c;SGVsbG8=\x07evil from 1.0.0 to\n 1.0.1")
	s := snapshot(evil)
	r := buildRows(s.Groups, "package", map[string]bool{})[0]
	lead, box, body, badges := rowParts(r, map[string]bool{}, map[string]bool{}, nil)
	out := layoutRow(lead, box, body, badges, 100)
	if strings.ContainsAny(out, "\x1b\x07\n") {
		t.Errorf("control characters leaked: %q", out)
	}
}

func TestRowStyleTints(t *testing.T) {
	s := fixture()
	rows := buildRows(s.Groups, "package", map[string]bool{})
	if rowStyle(rows[0], nil, false, nil).GetForeground() != styleYellow.GetForeground() {
		t.Error("blocked PR row is yellow")
	}
	if !rowStyle(rows[2], nil, true, nil).GetReverse() || !rowStyle(rows[2], nil, false, nil).GetBold() {
		t.Error("focused rows are reversed, group rows bold")
	}
}

func TestJobBadgesAndCheckbox(t *testing.T) {
	rows := buildRows(fixture().Groups, "repo", map[string]bool{}) // api#1, api#3, api#4, web#2
	pr := rows[0].pr
	jobs := func(j executor.Job) map[string]executor.Job {
		j.PR = pr
		return map[string]executor.Job{"acme/api#1": j}
	}
	_, box, _, badges := rowParts(rows[0], map[string]bool{}, map[string]bool{}, jobs(executor.Job{State: executor.JobQueued}))
	if box != iconQueued || badges[0] != iconQueued+" queued" {
		t.Errorf("queued: box=%q badges=%v", box, badges)
	}
	done := jobs(executor.Job{State: executor.JobDone, Result: model.Result{Status: model.ResultSuccess}})
	_, box, _, badges = rowParts(rows[0], map[string]bool{}, map[string]bool{}, done)
	if box != iconBoxOff || badges[0] != iconDone+" done" || !rowStyle(rows[0], nil, false, done).GetFaint() {
		t.Errorf("done: box=%q badges=%v (finished rows are dimmed)", box, badges)
	}
	failed := jobs(executor.Job{State: executor.JobDone, Result: model.Result{Status: model.ResultFailed}})
	if _, _, _, badges = rowParts(rows[0], map[string]bool{}, map[string]bool{}, failed); badges[0] != iconFailed+" failed" {
		t.Errorf("failed: badges=%v", badges)
	}
}
