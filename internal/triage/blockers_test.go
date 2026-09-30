package triage

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func codes(pr *model.PR) []string {
	out := []string{}
	for _, b := range pr.Blockers {
		out = append(out, b.Code)
	}
	return out
}

func actions(b model.Blocker) []string {
	out := []string{}
	for _, a := range b.SuggestedActions {
		out = append(out, a.Action)
	}
	return out
}

func diagnose(prs ...*model.PR) {
	Diagnose(GroupPRs(prs), testNow)
}

const lodash = "Bump lodash from 4.17.20 to 4.17.21"

func TestDiagnoseSinglePR(t *testing.T) {
	tests := []struct {
		name        string
		mod         func(*model.PR)
		wantCodes   []string
		wantActions []string // actions of the first blocker
		wantStatus  model.Status
	}{
		{"clean", func(*model.PR) {}, []string{}, nil, model.StatusReady},
		{"no checks", func(p *model.PR) { p.CheckRuns = nil }, []string{}, nil, model.StatusReady},
		{"pending with auto-merge", func(p *model.PR) {
			p.AutoMerge, p.MergeStateStatus = true, "BLOCKED"
			p.CheckRuns = []model.Check{checkRun("test", "IN_PROGRESS", "")}
		}, []string{}, nil, model.StatusMerging},
		{"real failure", func(p *model.PR) {
			p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "FAILURE")}
		}, []string{"checks_failing"}, []string{"fix"}, model.StatusBlocked},
		{"timed out is flaky", func(p *model.PR) {
			p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "TIMED_OUT")}
		}, []string{"checks_failing"}, []string{"rerun"}, model.StatusBlocked},
		{"flaky and real", func(p *model.PR) {
			p.CheckRuns = []model.Check{checkRun("a", "COMPLETED", "CANCELLED"), checkRun("b", "COMPLETED", "FAILURE")}
		}, []string{"checks_failing"}, []string{"rerun", "fix"}, model.StatusBlocked},
		{"behind", func(p *model.PR) { p.MergeStateStatus = "BEHIND" },
			[]string{"behind_base"}, []string{"rebase"}, model.StatusBlocked},
		{"dirty", func(p *model.PR) { p.MergeStateStatus = "DIRTY" },
			[]string{"conflicts"}, []string{"rebase", "recreate"}, model.StatusBlocked},
		{"conflicting", func(p *model.PR) { p.Mergeable = "CONFLICTING"; p.MergeStateStatus = "UNKNOWN" },
			[]string{"conflicts"}, []string{"rebase", "recreate"}, model.StatusBlocked},
		{"another review needed", func(p *model.PR) {
			p.ReviewDecision, p.MergeStateStatus = "REVIEW_REQUIRED", "BLOCKED"
			p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow}}
		}, []string{"review_required"}, []string{"request-review"}, model.StatusBlocked},
		{"viewer owes approval", func(p *model.PR) {
			p.ReviewDecision, p.MergeStateStatus = "REVIEW_REQUIRED", "BLOCKED"
		}, []string{}, nil, model.StatusReady},
		{"changes requested", func(p *model.PR) { p.ReviewDecision = "CHANGES_REQUESTED" },
			[]string{"changes_requested"}, []string{}, model.StatusBlocked},
		{"old", func(p *model.PR) { p.CreatedAt = testNow.Add(-31 * 24 * time.Hour) },
			[]string{"stale"}, []string{"recreate", "close"}, model.StatusBlocked},
		{"bot stopped updating", func(p *model.PR) {
			p.Body = "Renovate will not automatically rebase this PR, because it does not recognize the last commit author"
		}, []string{"stale"}, []string{"recreate", "close"}, model.StatusBlocked},
		{"blocked unknown", func(p *model.PR) {
			p.MergeStateStatus = "BLOCKED"
			p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow}}
		}, []string{"blocked_unknown"}, []string{}, model.StatusBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := testPR("acme/api", 1, lodash, tt.mod)
			diagnose(pr)
			if diff := cmp.Diff(tt.wantCodes, codes(pr)); diff != "" {
				t.Errorf("codes (-want +got):\n%s", diff)
			}
			if tt.wantActions != nil {
				if diff := cmp.Diff(tt.wantActions, actions(pr.Blockers[0])); diff != "" {
					t.Errorf("actions (-want +got):\n%s", diff)
				}
			}
			if pr.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", pr.Status, tt.wantStatus)
			}
		})
	}
}

func TestFixActionCarriesCommands(t *testing.T) {
	pr := testPR("acme/api", 12, lodash, func(p *model.PR) {
		p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "FAILURE")}
	})
	diagnose(pr)
	want := model.SuggestedAction{Action: "fix", Command: "gh dep-triage show acme/api#12 --logs", Checkout: "gh pr checkout 12 -R acme/api"}
	if diff := cmp.Diff(want, pr.Blockers[0].SuggestedActions[0]); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestFlakyWhenSameCheckPassesInGroup(t *testing.T) {
	failing := testPR("acme/web", 2, lodash, func(p *model.PR) {
		p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "FAILURE")}
	})
	passing := testPR("acme/api", 1, lodash, func(p *model.PR) {
		p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "SUCCESS")}
	})
	diagnose(failing, passing)
	if diff := cmp.Diff([]string{"rerun"}, actions(failing.Blockers[0])); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if got := failing.Blockers[0].SuggestedActions[0].Command; got != "gh dep-triage rerun acme/web#2 --yes" {
		t.Errorf("command = %q", got)
	}
}

func TestSuperseded(t *testing.T) {
	older := testPR("acme/api", 1, "Bump lodash from 4.17.19 to 4.17.20")
	newer := testPR("acme/api", 2, lodash)
	otherRepo := testPR("acme/web", 3, "Bump lodash from 4.17.19 to 4.17.20")
	diagnose(older, newer, otherRepo)
	if diff := cmp.Diff([]string{"superseded"}, codes(older)); diff != "" {
		t.Errorf("older (-want +got):\n%s", diff)
	}
	if got := older.Blockers[0].SuggestedActions[0].Command; got != "gh dep-triage close acme/api#1 --reason superseded --yes" {
		t.Errorf("command = %q", got)
	}
	if len(newer.Blockers) != 0 || len(otherRepo.Blockers) != 0 {
		t.Errorf("newer=%v other=%v", codes(newer), codes(otherRepo))
	}
}

func TestBuildInfersBeforeGrouping(t *testing.T) {
	a := testPR("a/x", 1, "Bump react from 18.3.0 to 19.0.0")
	b := testPR("a/y", 2, "update dependency react to v19.0.0")
	groups := Build([]*model.PR{a, b}, testNow)
	if len(groups) != 1 || groups[0].ID != "group:react@19.0.0" || len(groups[0].PRs) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if a.Status != model.StatusReady {
		t.Errorf("status = %q", a.Status)
	}
}
