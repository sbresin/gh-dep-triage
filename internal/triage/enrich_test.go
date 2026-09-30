package triage

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestClassifyCheck(t *testing.T) {
	tests := []struct {
		check model.Check
		want  string
	}{
		{checkRun("a", "COMPLETED", "SUCCESS"), model.CheckStatePassed},
		{checkRun("a", "IN_PROGRESS", ""), model.CheckStatePending},
		{checkRun("a", "QUEUED", ""), model.CheckStatePending},
		{checkRun("a", "COMPLETED", "FAILURE"), model.CheckStateFailed},
		{checkRun("a", "COMPLETED", "TIMED_OUT"), model.CheckStateFailed},
		{checkRun("a", "COMPLETED", "CANCELLED"), model.CheckStateFailed},
		{checkRun("a", "COMPLETED", "STARTUP_FAILURE"), model.CheckStateFailed},
		{checkRun("a", "COMPLETED", "ACTION_REQUIRED"), model.CheckStateFailed},
		{checkRun("a", "COMPLETED", "NEUTRAL"), model.CheckStateSkipped},
		{checkRun("a", "COMPLETED", "SKIPPED"), model.CheckStateSkipped},
		{statusCtx("s", "SUCCESS"), model.CheckStatePassed},
		{statusCtx("s", "PENDING"), model.CheckStatePending},
		{statusCtx("s", "EXPECTED"), model.CheckStatePending},
		{statusCtx("s", "ERROR"), model.CheckStateFailed},
		{statusCtx("s", "FAILURE"), model.CheckStateFailed},
	}
	for _, tt := range tests {
		if got := ClassifyCheck(tt.check); got != tt.want {
			t.Errorf("ClassifyCheck(%+v) = %q, want %q", tt.check, got, tt.want)
		}
	}
}

func TestSummarizeChecks(t *testing.T) {
	checks := []model.Check{
		checkRun("build", "COMPLETED", "SUCCESS"),
		checkRun("test", "COMPLETED", "FAILURE"),
		checkRun("lint", "IN_PROGRESS", ""),
		statusCtx("legacy", "SUCCESS"),
		checkRun("docs", "COMPLETED", "SKIPPED"),
	}
	got := SummarizeChecks(checks)
	want := model.CheckSummary{Total: 5, Passed: 2, Skipped: 1, Failed: 1, Pending: 1,
		FailedNames: []string{"test"}, PendingNames: []string{"lint"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if checks[1].State != model.CheckStateFailed {
		t.Errorf("state not set in place: %q", checks[1].State)
	}
}

func TestSummarizeChecksEmpty(t *testing.T) {
	got := SummarizeChecks(nil)
	if got.FailedNames == nil || got.PendingNames == nil {
		t.Errorf("name slices must be non-nil: %+v", got)
	}
}

func TestViewerApproved(t *testing.T) {
	t1, t2 := testNow.Add(-2*time.Hour), testNow.Add(-time.Hour)
	r := func(state string, at time.Time) model.Review { return model.Review{State: state, SubmittedAt: at} }
	tests := []struct {
		name    string
		reviews []model.Review
		want    bool
	}{
		{"none", nil, false},
		{"approved", []model.Review{r("APPROVED", t1)}, true},
		{"comment after approval keeps it", []model.Review{r("APPROVED", t1), r("COMMENTED", t2)}, true},
		{"changes requested after approval", []model.Review{r("APPROVED", t1), r("CHANGES_REQUESTED", t2)}, false},
		{"approval after changes requested", []model.Review{r("CHANGES_REQUESTED", t1), r("APPROVED", t2)}, true},
		{"dismissed after approval", []model.Review{r("APPROVED", t1), r("DISMISSED", t2)}, false},
	}
	for _, tt := range tests {
		if got := ViewerApproved(tt.reviews); got != tt.want {
			t.Errorf("%s: got %v", tt.name, got)
		}
	}
}

func TestEnrich(t *testing.T) {
	p := &model.PR{Title: "Bump lodash from 4.17.20 to 4.17.21",
		ViewerReviews: []model.Review{{State: "APPROVED", SubmittedAt: testNow}}}
	Enrich(p)
	if p.Package != "lodash" || p.PackageKey != "lodash" || p.SourceVersion != "4.17.20" ||
		p.TargetVersion != "4.17.21" || p.Bump != model.BumpPatch || !p.ViewerApproved {
		t.Errorf("unexpected enrichment: %+v", p)
	}
	if p.RequestedReviewers == nil || p.Blockers == nil || p.CheckRuns == nil {
		t.Errorf("slices must be non-nil: %+v", p)
	}
}
