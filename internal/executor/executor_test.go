package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

// enriched returns the fake's PR enriched, as the CLI would hand it over from
// a snapshot.
func enriched(f *githubtest.Fake, ref string) *model.PR {
	cp := *f.PRs[ref]
	cp.CheckRuns = append([]model.Check(nil), cp.CheckRuns...)
	triage.Enrich(&cp)
	return &cp
}

func task(f *githubtest.Fake, action, ref string) plan.Task {
	return plan.Task{Action: action, PR: enriched(f, ref)}
}

type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) fn(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return ctx.Err()
}

func opts(s *sleeps) Options { return Options{Viewer: "octocat", Sleep: s.fn} }

func statuses(rs []model.Result) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Ref+" "+r.Status+" "+r.Reason)
	}
	return out
}

func TestApprove(t *testing.T) {
	f := githubtest.NewFake("octocat")
	pending := githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	done := githubtest.NewPR("acme/api", 2, "Bump b from 1.0.0 to 1.0.1")
	done.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: time.Now()}}
	f.Add(pending, true, false)
	f.Add(done, true, false)

	var s sleeps
	got := Run(context.Background(), f, []plan.Task{task(f, "approve", "acme/api#1"), task(f, "approve", "acme/api#2")}, opts(&s))
	want := []model.Result{
		{Action: "approve", Ref: "acme/api#1", Status: model.ResultSuccess, Steps: []string{"approved"}, HeadOid: "sha1"},
		{Action: "approve", Ref: "acme/api#2", Status: model.ResultSkipped, Reason: model.ReasonAlreadyApproved, Steps: []string{}, HeadOid: "sha2"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]time.Duration{DefaultDelay}, s.d); diff != "" {
		t.Errorf("one delay between the two same-repo PRs (-want +got):\n%s", diff)
	}
}

func TestApproveMutationError(t *testing.T) {
	f := githubtest.NewFake("octocat")
	f.Add(githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	f.MutationErr["approve acme/api#1"] = errors.New("Resource not accessible by integration")
	var s sleeps
	got := Run(context.Background(), f, []plan.Task{task(f, "approve", "acme/api#1")}, opts(&s))
	if got[0].Status != model.ResultFailed || got[0].Reason != model.ReasonMutationFailed || got[0].Message != "Resource not accessible by integration" {
		t.Errorf("got %+v", got[0])
	}
}

func TestPresetResultsPassThroughInOrder(t *testing.T) {
	f := githubtest.NewFake("octocat")
	f.Add(githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	pre := &model.Result{Action: "merge", Ref: "acme/api#9", Status: model.ResultFailed, Reason: model.ReasonNotEligible, Steps: []string{}}
	var s sleeps
	var seen []string
	o := opts(&s)
	o.OnResult = func(r model.Result) { seen = append(seen, r.Ref) }
	got := Run(context.Background(), f, []plan.Task{{Action: "merge", Result: pre}, task(f, "approve", "acme/api#1")}, o)
	if diff := cmp.Diff([]string{"acme/api#9 failed not_eligible", "acme/api#1 success "}, statuses(got)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if len(seen) != 2 {
		t.Errorf("OnResult calls = %v", seen)
	}
}

// gate blocks Approve calls until released and tracks concurrency.
type gate struct {
	*githubtest.Fake
	mu        sync.Mutex
	inFlight  int
	maxFlight int
	release   chan struct{}
}

func (g *gate) Approve(ctx context.Context, id, head string) error {
	g.mu.Lock()
	g.inFlight++
	g.maxFlight = max(g.maxFlight, g.inFlight)
	g.mu.Unlock()
	<-g.release
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return g.Fake.Approve(ctx, id, head)
}

func TestAtMostTwoReposInParallel(t *testing.T) {
	f := githubtest.NewFake("octocat")
	var tasks []plan.Task
	for i, repo := range []string{"acme/a", "acme/b", "acme/c", "acme/d"} {
		f.Add(githubtest.NewPR(repo, i+1, "Bump a from 1.0.0 to 1.0.1"), true, false)
		tasks = append(tasks, task(f, "approve", fmt.Sprintf("%s#%d", repo, i+1)))
	}
	g := &gate{Fake: f, release: make(chan struct{})}
	done := make(chan []model.Result)
	var s sleeps
	go func() { done <- Run(context.Background(), g, tasks, opts(&s)) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		n := g.inFlight
		g.mu.Unlock()
		if n == DefaultRepoParallel || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for range tasks {
		g.release <- struct{}{}
	}
	got := <-done
	if g.maxFlight != DefaultRepoParallel {
		t.Errorf("max in flight = %d, want %d", g.maxFlight, DefaultRepoParallel)
	}
	for _, r := range got {
		if r.Status != model.ResultSuccess {
			t.Errorf("%+v", r)
		}
	}
}

func TestRunCancelStopsQueuedItems(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 3; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump p%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := Options{Viewer: "octocat", Sleep: func(ctx context.Context, _ time.Duration) error {
		cancel() // Ctrl-C arrives during the delay after the first PR
		return ctx.Err()
	}}
	got := Run(ctx, f, []plan.Task{task(f, "approve", "acme/api#1"), task(f, "approve", "acme/api#2"), task(f, "approve", "acme/api#3")}, o)
	want := []string{"acme/api#1 success ", "acme/api#2 skipped cancelled", "acme/api#3 skipped cancelled"}
	if diff := cmp.Diff(want, statuses(got)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if len(f.Calls) != 1 {
		t.Errorf("calls after cancel = %v", f.Calls)
	}
}
