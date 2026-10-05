package loader

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func opts() Options { return Options{Limit: 200, Workers: 2, Now: func() time.Time { return now }} }

func refs(s *model.Snapshot) []string {
	out := []string{}
	for _, pr := range s.PRs() {
		out = append(out, pr.Ref)
	}
	sort.Strings(out)
	return out
}

func TestLoadFiltersAndFlags(t *testing.T) {
	f := githubtest.NewFake("octocat")
	requested := githubtest.NewPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21")
	approved := githubtest.NewPR("acme/api", 2, "Bump axios from 1.6.0 to 1.7.0")
	approved.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: now.Add(-time.Hour)}}
	retracted := githubtest.NewPR("acme/api", 3, "Bump chalk from 5.0.0 to 5.1.0")
	retracted.ViewerReviews = []model.Review{
		{State: "APPROVED", SubmittedAt: now.Add(-2 * time.Hour)},
		{State: "CHANGES_REQUESTED", SubmittedAt: now.Add(-time.Hour)},
	}
	draft := githubtest.NewPR("acme/api", 4, "Bump vite from 5.0.0 to 5.1.0")
	draft.IsDraft = true
	renovate := githubtest.NewPR("acme/web", 5, "fix(deps): update dependency react to v19.0.1")
	renovate.Author = "renovate"
	human := githubtest.NewPR("acme/web", 6, "Bump my feature")
	human.Author = "alice"

	f.Add(requested, true, false)
	f.Add(approved, false, true)
	f.Add(retracted, false, true)
	f.Add(draft, true, false)
	f.Add(renovate, true, true)
	f.Add(human, true, false)
	f.Requested[len(f.Requested)-1].AuthorType = "User"

	snap, err := Load(context.Background(), f, opts())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"acme/api#1", "acme/api#2", "acme/web#5"}, refs(snap)); diff != "" {
		t.Errorf("kept (-want +got):\n%s", diff)
	}
	for _, b := range f.Batches {
		for _, r := range b {
			if r.String() == "acme/web#6" {
				t.Error("human PR must not be fetched")
			}
		}
	}
	got := snap.Find(model.PRRef{Repo: "acme/web", Number: 5})
	if !got.RequestedForReview || !got.SeenInReviewedSearch {
		t.Errorf("flags: %+v", got)
	}
	if a := snap.Find(model.PRRef{Repo: "acme/api", Number: 2}); a.RequestedForReview || !a.ViewerApproved {
		t.Errorf("approved flags: %+v", a)
	}
	if snap.Viewer != "octocat" || !snap.GeneratedAt.Equal(now) || snap.Warnings == nil {
		t.Errorf("snapshot meta: %+v", snap)
	}
}

func TestLoadBatchesWithinWorkerLimit(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 60; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump pkg%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	snap, err := Load(context.Background(), f, opts())
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{}
	for _, b := range f.Batches {
		sizes = append(sizes, len(b))
	}
	sort.Ints(sizes)
	want := make([]int, 15)
	for i := range want {
		want[i] = 4
	}
	if diff := cmp.Diff(want, sizes); diff != "" {
		t.Errorf("batch sizes (-want +got):\n%s", diff)
	}
	if len(snap.PRs()) != 60 {
		t.Errorf("loaded %d PRs", len(snap.PRs()))
	}
}

func TestDefaultWorkers(t *testing.T) {
	if got := (Options{}).withDefaults().Workers; got != 16 {
		t.Errorf("default workers = %d, want 16", got)
	}
}

func TestLoadRetriesFailedBatchPerPR(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 8; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump pkg%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	f.FailOnce["acme/api#3"] = errors.New("HTTP 504: We couldn't respond to your request in time")

	snap, err := Load(context.Background(), f, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.PRs()) != 8 || len(snap.Warnings) != 0 {
		t.Errorf("loaded %d PRs, warnings %+v; want 8 and none", len(snap.PRs()), snap.Warnings)
	}
	singles := 0
	for _, b := range f.Batches {
		if len(b) == 1 {
			singles++
		}
	}
	if singles != 4 {
		t.Errorf("single-PR retries = %d, want 4 (one per PR of the failed batch)", singles)
	}
}

func TestLoadBatchFailureBecomesWarnings(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 30; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump pkg%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	f.FailBatch["acme/api#30"] = errors.New("HTTP 502")
	delete(f.PRs, "acme/api#3") // per-PR not_found warning in a healthy batch

	snap, err := Load(context.Background(), f, opts())
	if err != nil {
		t.Fatal(err)
	}
	// #30's batch fails; the per-PR retry recovers #29 but #30 keeps failing.
	if len(snap.PRs()) != 28 {
		t.Errorf("loaded %d PRs, want 28", len(snap.PRs()))
	}
	codes := map[string]int{}
	for _, w := range snap.Warnings {
		codes[w.Code]++
	}
	if codes["fetch_failed"] != 1 || codes["not_found"] != 1 {
		t.Errorf("warnings = %+v", snap.Warnings)
	}
}

func TestLoadCancelledReturnsError(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 30; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump pkg%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snap, err := Load(ctx, f, opts())
	if !errors.Is(err, context.Canceled) || snap != nil {
		t.Errorf("snap=%v err=%v, want context.Canceled", snap, err)
	}
	if len(f.Batches) != 0 {
		t.Errorf("dispatched %d batches after cancellation", len(f.Batches))
	}
}

func TestLoadTeamQuery(t *testing.T) {
	f := githubtest.NewFake("octocat")
	o := opts()
	o.Team = "acme/platform"
	if _, err := Load(context.Background(), f, o); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.Queries, "\n")
	if !strings.Contains(joined, "team-review-requested:acme/platform") || !strings.Contains(joined, "reviewed-by:@me") {
		t.Errorf("queries = %q", f.Queries)
	}
}

func TestLoadEmpty(t *testing.T) {
	snap, err := Load(context.Background(), githubtest.NewFake("octocat"), opts())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Groups == nil || len(snap.Groups) != 0 || snap.Warnings == nil {
		t.Errorf("empty snapshot = %+v", snap)
	}
}
