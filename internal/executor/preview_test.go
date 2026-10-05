package executor

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

const checkbox = "- [ ] <!-- rebase-check --> rebase"

func previewPR(mods ...func(*model.PR)) *model.PR {
	p := githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	for _, m := range mods {
		m(p)
	}
	triage.Enrich(p)
	return p
}

func TestPreview(t *testing.T) {
	failedRun := func(p *model.PR) {
		c := githubtest.CheckRun("test", "COMPLETED", "FAILURE", 1)
		c.WorkflowRunID = 55
		p.CheckRuns = []model.Check{c}
	}
	tests := []struct {
		name   string
		action string
		args   map[string]string
		pr     *model.PR
		status string
		reason string
		steps  []string
	}{
		{"merge owes approval", "merge", nil, previewPR(), model.ResultPlanned, "",
			[]string{"approve", "merge (squash) if clean, else enable auto-merge (squash)"}},
		{"merge owes approval, no auto-merge", "merge", nil, previewPR(func(p *model.PR) { p.RepoSettings.AutoMergeAllowed = false }),
			model.ResultPlanned, "", []string{"approve", "merge (squash) if clean, else skip"}},
		{"merge approved clean", "merge", nil, previewPR(approved, func(p *model.PR) { p.MergeStateStatus = "CLEAN" }),
			model.ResultPlanned, "", []string{"merge (squash)"}},
		{"merge approved pending", "merge", nil, previewPR(approved), model.ResultPlanned, "", []string{"enable auto-merge (squash)"}},
		{"merge approved, no auto-merge", "merge", nil, previewPR(approved, func(p *model.PR) { p.RepoSettings.AutoMergeAllowed = false }),
			model.ResultSkipped, model.ReasonNotMergeable, []string{}},
		{"merge already merging", "merge", nil, previewPR(func(p *model.PR) { p.AutoMerge = true }), model.ResultSkipped, model.ReasonAlreadyMerging, []string{}},
		{"merge conflicts", "merge", nil, previewPR(func(p *model.PR) { p.MergeStateStatus = "DIRTY" }), model.ResultSkipped, model.ReasonNotMergeable, []string{}},
		{"approve", "approve", nil, previewPR(), model.ResultPlanned, "", []string{"approve"}},
		{"approve already approved", "approve", nil, previewPR(approved), model.ResultSkipped, model.ReasonAlreadyApproved, []string{}},
		{"rebase dependabot", "rebase", nil, previewPR(), model.ResultPlanned, "", []string{"comment @dependabot rebase"}},
		{"recreate renovate", "recreate", nil, previewPR(func(p *model.PR) { p.Author, p.Body = "renovate", checkbox }),
			model.ResultPlanned, "", []string{"tick the rebase checkbox"}},
		{"rebase renovate without checkbox", "rebase", nil, previewPR(func(p *model.PR) { p.Author = "renovate" }),
			model.ResultFailed, model.ReasonNoRebaseCheckbox, []string{}},
		{"rerun", "rerun", nil, previewPR(failedRun), model.ResultPlanned, "", []string{"re-run workflow run 55"}},
		{"rerun nothing failed", "rerun", nil, previewPR(), model.ResultSkipped, model.ReasonNoFailedChecks, []string{}},
		{"request review", "request-review", map[string]string{"reviewer": "acme/platform"}, previewPR(),
			model.ResultPlanned, "", []string{"request review from acme/platform"}},
		{"close", "close", map[string]string{"reason": "stale"}, previewPR(), model.ResultPlanned, "", []string{"comment", "close"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Preview(tt.action, tt.pr, tt.args)
			if r.Status != tt.status || r.Reason != tt.reason || !cmp.Equal(r.Steps, tt.steps) || r.HeadOid != "sha1" {
				t.Errorf("got %s/%s %v (head %s), want %s/%s %v", r.Status, r.Reason, r.Steps, r.HeadOid, tt.status, tt.reason, tt.steps)
			}
		})
	}
}

// A dry run must predict the same stop reason the real run reports.
func TestPreviewAgreesWithRun(t *testing.T) {
	tests := []struct {
		name   string
		action string
		mod    func(*model.PR)
	}{
		{"already merging", "merge", func(p *model.PR) { p.AutoMerge = true }},
		{"conflicts", "merge", func(p *model.PR) { p.MergeStateStatus = "DIRTY" }},
		{"already approved", "approve", approved},
		{"no checkbox", "rebase", func(p *model.PR) { p.Author = "renovate" }},
		{"nothing to rerun", "rerun", func(*model.PR) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake(tt.mod)
			j := job(f, tt.action, "acme/api#1")
			want := Preview(tt.action, j.PR, nil)
			got, _ := runAll(t, context.Background(), f, opts(&sleeps{}), j)
			if got[0].Status != want.Status || got[0].Reason != want.Reason {
				t.Errorf("run %s/%s, preview %s/%s", got[0].Status, got[0].Reason, want.Status, want.Reason)
			}
		})
	}
}
