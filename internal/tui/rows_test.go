package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
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
	if prCheckbox(find("acme/api#3"), sel) != iconBoxBlocked || prCheckbox(find("acme/api#4"), sel) != iconBoxBlocked {
		t.Error("blocked and merging PRs show the blocked checkbox")
	}
	if prCheckbox(find("acme/api#1"), sel) != iconBoxOn || prCheckbox(find("acme/web#2"), sel) != iconBoxOff {
		t.Error("ready PR checkboxes follow selection")
	}
	var lodash *model.Group
	for _, g := range s.Groups {
		if g.ID == "group:lodash@4.17.21" {
			lodash = g
		}
	}
	if groupCheckbox(lodash, sel) != iconBoxPartial {
		t.Error("half-selected group shows partial")
	}
	if diff := cmp.Diff([]string{"sel 1/2", iconCheckOK}, groupBadges(lodash, sel)); diff != "" {
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
	lead, box, body, badges := rowParts(r, map[string]bool{}, map[string]bool{})
	out := layoutRow(lead, box, body, badges, 100)
	if strings.ContainsAny(out, "\x1b\x07\n") {
		t.Errorf("control characters leaked: %q", out)
	}
}

func TestRowStyleTints(t *testing.T) {
	s := fixture()
	rows := buildRows(s.Groups, "package", map[string]bool{})
	if rowStyle(rows[0], nil, false).GetForeground() != styleYellow.GetForeground() {
		t.Error("blocked PR row is yellow")
	}
	if !rowStyle(rows[2], nil, true).GetReverse() || !rowStyle(rows[2], nil, false).GetBold() {
		t.Error("focused rows are reversed, group rows bold")
	}
}

func TestRiskBadges(t *testing.T) {
	s := fixture()
	lodash := &model.Risk{SourceRepo: "github.com/lodash/lodash", Stars: 61277, Scorecard: 7.5}
	var behind *model.PR
	for _, p := range s.PRs() {
		switch p.Ref {
		case "acme/api#3":
			behind = p
			p.Risk = &model.Risk{Stars: 108000, Scorecard: 6.9}
		case "acme/web#2":
			p.Risk = lodash
		}
	}
	if diff := cmp.Diff([]string{"behind", "108k★ 6.9", "review", "--"}, prBadges(behind)); diff != "" {
		t.Errorf("PR badges (-want +got):\n%s", diff)
	}
	for _, g := range s.Groups {
		if g.ID == "group:lodash@4.17.21" {
			if diff := cmp.Diff([]string{"61k★ 7.5", "sel 0/2", iconCheckOK}, groupBadges(g, nil)); diff != "" {
				t.Errorf("group badges take the first PR with risk data (-want +got):\n%s", diff)
			}
		}
	}
}

func TestMaliciousRowIsRed(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.Risk = &model.Risk{Findings: []string{model.FindingMalicious}}
	})
	if rowStyle(row{pr: pr}, nil, false).GetForeground() != styleRed.GetForeground() {
		t.Error("a PR flagged MALICIOUS is red")
	}
}
