package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func mergeOne(t *testing.T, f *githubtest.Fake, ref string) (model.Result, *sleeps) {
	t.Helper()
	s := &sleeps{}
	got, _ := runAll(t, context.Background(), f, opts(s), job(f, "merge", ref))
	return got[0], s
}

func newMergeFake(mods ...func(*model.PR)) *githubtest.Fake {
	f := githubtest.NewFake("octocat")
	pr := githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	for _, m := range mods {
		m(pr)
	}
	f.Add(pr, true, false)
	return f
}

func approved(p *model.PR) {
	p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: time.Now()}}
	p.ReviewDecision = "APPROVED"
}

func TestMergeApprovesThenMergesWhenClean(t *testing.T) {
	f := newMergeFake()
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	r, _ := mergeOne(t, f, "acme/api#1")
	want := model.Result{Action: "merge", Ref: "acme/api#1", Status: model.ResultSuccess, Steps: []string{"approved", "merged (squash)"}, HeadOid: "sha1"}
	if diff := cmp.Diff(want, r); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1", "merge acme/api#1 sha1 SQUASH"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestMergeEnablesAutoMergeWhilePending(t *testing.T) {
	f := newMergeFake(approved, func(p *model.PR) {
		p.CheckRuns = []model.Check{githubtest.CheckRun("test", "IN_PROGRESS", "", 1)}
	})
	r, _ := mergeOne(t, f, "acme/api#1")
	if r.Status != model.ResultSuccess || !cmp.Equal(r.Steps, []string{"auto-merge enabled (squash)"}) {
		t.Errorf("got %+v", r)
	}
	if diff := cmp.Diff([]string{"automerge acme/api#1 sha1 SQUASH"}, f.Calls); diff != "" {
		t.Errorf("approved PR needs no approve (-want +got):\n%s", diff)
	}
}

// pollFake mutates the stored PR before each FetchPRs, simulating GitHub
// finishing its mergeability computation.
type pollFake struct {
	*githubtest.Fake
	onFetch func(*model.PR)
}

func (p *pollFake) FetchPRs(ctx context.Context, viewer string, refs []model.PRRef) ([]*model.PR, []model.Problem, error) {
	p.onFetch(p.PRs[refs[0].String()])
	return p.Fake.FetchPRs(ctx, viewer, refs)
}

func TestMergePollsUnknownThenMerges(t *testing.T) {
	f := newMergeFake()
	polls := 0
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "UNKNOWN" }
	wrapped := &pollFake{Fake: f, onFetch: func(p *model.PR) {
		polls++
		if polls == 4 { // fetch 1 is the pre-run check; 2 and 3 still see UNKNOWN
			p.MergeStateStatus = "CLEAN"
		}
	}}
	s := &sleeps{}
	got, _ := runAll(t, context.Background(), wrapped, opts(s), job(f, "merge", "acme/api#1"))
	if got[0].Status != model.ResultSuccess || got[0].Steps[1] != "merged (squash)" {
		t.Errorf("got %+v", got[0])
	}
	if diff := cmp.Diff([]time.Duration{DefaultPollInterval, DefaultPollInterval}, s.got()); diff != "" {
		t.Errorf("poll sleeps (-want +got):\n%s", diff)
	}
}

func TestMergeSkips(t *testing.T) {
	tests := []struct {
		name   string
		mod    func(*model.PR)
		reason string
	}{
		{"already merging", func(p *model.PR) { p.AutoMerge = true }, model.ReasonAlreadyMerging},
		{"merge queue", func(p *model.PR) { p.MergeQueue = true }, model.ReasonMergeQueue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake(tt.mod)
			r, _ := mergeOne(t, f, "acme/api#1")
			if r.Status != model.ResultSkipped || r.Reason != tt.reason || len(f.Calls) != 0 {
				t.Errorf("got %+v calls %v", r, f.Calls)
			}
		})
	}
}

func TestMergePreSkipsUseFreshData(t *testing.T) {
	f := newMergeFake()
	j := job(f, "merge", "acme/api#1") // confirmed while auto-merge was off
	f.PRs["acme/api#1"].AutoMerge = true
	got, _ := runAll(t, context.Background(), f, opts(&sleeps{}), j)
	if got[0].Status != model.ResultSkipped || got[0].Reason != model.ReasonAlreadyMerging || len(f.Calls) != 0 {
		t.Errorf("got %+v calls %v", got[0], f.Calls)
	}
}

func TestMergeNotMergeableCases(t *testing.T) {
	tests := []struct {
		name   string
		mod    func(*model.PR)
		reason string
	}{
		{"blocked, auto-merge disabled", func(p *model.PR) {
			approved(p)
			p.MergeStateStatus = "BLOCKED"
			p.RepoSettings.AutoMergeAllowed = false
		}, model.ReasonNotMergeable},
		{"conflicts, not yet approved", func(p *model.PR) { p.MergeStateStatus = "DIRTY" }, model.ReasonNotMergeable},
		{"no merge method, not yet approved", func(p *model.PR) {
			p.MergeStateStatus = "CLEAN"
			p.RepoSettings = model.RepoSettings{AutoMergeAllowed: true}
		}, model.ReasonNoMergeMethod},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake(tt.mod)
			r, _ := mergeOne(t, f, "acme/api#1")
			if r.Status != model.ResultSkipped || r.Reason != tt.reason || r.Message == "" {
				t.Errorf("got %+v", r)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no mutation expected, got %v", f.Calls)
			}
		})
	}
}

func TestMergeFailuresAfterApproval(t *testing.T) {
	tests := []struct {
		name   string
		hook   func(*model.PR)
		status string
		reason string
	}{
		{"head moved", func(p *model.PR) { p.HeadOid = "sha-new" }, model.ResultFailed, model.ReasonHeadChanged},
		{"check failed", func(p *model.PR) {
			p.CheckRuns = []model.Check{githubtest.CheckRun("test", "COMPLETED", "FAILURE", 1)}
		}, model.ResultFailed, model.ReasonChecksFailing},
		{"auto-merge turned on", func(p *model.PR) { p.AutoMerge = true }, model.ResultSkipped, model.ReasonAlreadyMerging},
		{"merge queue turned on", func(p *model.PR) { p.MergeQueue = true }, model.ResultSkipped, model.ReasonMergeQueue},
		{"merged meanwhile", func(p *model.PR) { p.State = "MERGED" }, model.ResultSkipped, model.ReasonNotOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake()
			f.AfterApprove["acme/api#1"] = tt.hook
			r, _ := mergeOne(t, f, "acme/api#1")
			if r.Status != tt.status || r.Reason != tt.reason || !cmp.Equal(r.Steps, []string{"approved"}) {
				t.Errorf("got %+v", r)
			}
			if tt.reason == model.ReasonNotOpen && r.Message != "pull request is merged" {
				t.Errorf("message = %q", r.Message)
			}
			if len(f.Calls) != 1 {
				t.Errorf("only the approval may have run: %v", f.Calls)
			}
		})
	}
}

func TestMergeRefetchFailure(t *testing.T) {
	f := newMergeFake()
	f.AfterApprove["acme/api#1"] = func(*model.PR) { f.FailBatch["acme/api#1"] = errors.New("HTTP 502") }
	r, _ := mergeOne(t, f, "acme/api#1")
	if r.Status != model.ResultFailed || r.Reason != model.ReasonRefetchFailed || !cmp.Equal(r.Steps, []string{"approved"}) {
		t.Errorf("got %+v", r)
	}
}

func TestMergePollsExhaustedEnablesAutoMerge(t *testing.T) {
	f := newMergeFake()
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "UNKNOWN" }
	r, s := mergeOne(t, f, "acme/api#1")
	if r.Status != model.ResultSuccess || !cmp.Equal(r.Steps, []string{"approved", "auto-merge enabled (squash)"}) {
		t.Errorf("got %+v", r)
	}
	if diff := cmp.Diff([]time.Duration{DefaultPollInterval, DefaultPollInterval, DefaultPollInterval}, s.got()); diff != "" {
		t.Errorf("poll sleeps (-want +got):\n%s", diff)
	}
}

func TestMergeCancelledBeforeFurtherMutations(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *githubtest.Fake, cancel context.CancelFunc, s *sleeps)
	}{
		{"during poll sleep", func(f *githubtest.Fake, cancel context.CancelFunc, s *sleeps) {
			f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "UNKNOWN" }
			s.hook = func(time.Duration) { cancel() }
		}},
		{"before refetch", func(f *githubtest.Fake, cancel context.CancelFunc, _ *sleeps) {
			f.AfterApprove["acme/api#1"] = func(p *model.PR) {
				p.MergeStateStatus = "CLEAN"
				cancel()
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &sleeps{}
			tt.setup(f, cancel, s)
			got, _ := runAll(t, ctx, f, opts(s), job(f, "merge", "acme/api#1"))
			r := got[0]
			if r.Status != model.ResultSkipped || r.Reason != model.ReasonCancelled || !cmp.Equal(r.Steps, []string{"approved"}) {
				t.Errorf("got %+v", r)
			}
			if diff := cmp.Diff([]string{"approve acme/api#1 sha1"}, f.Calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMergeMutationFailureKeepsSteps(t *testing.T) {
	f := newMergeFake()
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	f.MutationErr["merge acme/api#1"] = errors.New("Head branch was modified. Review and try the merge again.")
	r, _ := mergeOne(t, f, "acme/api#1")
	if r.Status != model.ResultFailed || r.Reason != model.ReasonMutationFailed ||
		!cmp.Equal(r.Steps, []string{"approved"}) || r.Message == "" {
		t.Errorf("got %+v", r)
	}
}

func TestMergeMethod(t *testing.T) {
	tests := []struct {
		s    model.RepoSettings
		want string
	}{
		{model.RepoSettings{MergeCommitAllowed: true, SquashMergeAllowed: true, ViewerDefaultMergeMethod: "MERGE"}, "MERGE"},
		{model.RepoSettings{MergeCommitAllowed: true, SquashMergeAllowed: true, ViewerDefaultMergeMethod: "REBASE"}, "SQUASH"},
		{model.RepoSettings{MergeCommitAllowed: true, RebaseMergeAllowed: true}, "MERGE"},
		{model.RepoSettings{RebaseMergeAllowed: true}, "REBASE"},
		{model.RepoSettings{}, ""},
	}
	for _, tt := range tests {
		if got := MergeMethod(tt.s); got != tt.want {
			t.Errorf("MergeMethod(%+v) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

type blockingFake struct{ *githubtest.Fake }

func (b *blockingFake) Merge(ctx context.Context, _, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestMergeMutationTimesOut(t *testing.T) {
	f := newMergeFake(approved, func(p *model.PR) { p.MergeStateStatus = "CLEAN" })
	o := opts(&sleeps{})
	o.MutationTimeout = 10 * time.Millisecond
	done := make(chan model.Result)
	go func() {
		got, _ := runAll(t, context.Background(), &blockingFake{f}, o, job(f, "merge", "acme/api#1"))
		done <- got[0]
	}()
	select {
	case r := <-done:
		if r.Status != model.ResultFailed || r.Reason != model.ReasonMutationFailed || !strings.Contains(r.Message, "deadline") {
			t.Errorf("got %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the job did not finish: the mutation has no timeout")
	}
}
