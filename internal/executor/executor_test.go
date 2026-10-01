package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

// enriched returns the fake's PR enriched, as a snapshot would hand it over.
func enriched(f *githubtest.Fake, ref string) *model.PR {
	cp := *f.PRs[ref]
	cp.CheckRuns = append([]model.Check(nil), cp.CheckRuns...)
	triage.Enrich(&cp)
	return &cp
}

// job is what the user confirmed: the PR as it is in the fake right now.
func job(f *githubtest.Fake, action, ref string) Job {
	return Job{Action: action, PR: enriched(f, ref)}
}

// sleeps records Sleep calls; hook, if set, runs inside each call.
type sleeps struct {
	mu   sync.Mutex
	d    []time.Duration
	hook func(time.Duration)
}

func (s *sleeps) fn(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(d)
	}
	return ctx.Err()
}

func (s *sleeps) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func opts(s *sleeps) Options { return Options{Viewer: "octocat", Sleep: s.fn} }

func statuses(rs []model.Result) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Ref+" "+r.Status+" "+r.Reason)
	}
	return out
}

// collect drains q's events until the channel is closed.
func collect(q *Queue) <-chan []Event {
	ch := make(chan []Event, 1)
	go func() {
		var evs []Event
		for e := range q.Events() {
			evs = append(evs, e)
		}
		ch <- evs
	}()
	return ch
}

// runAll queues jobs while paused (so they start in submission order),
// closes the queue without cancelling and returns the results in submission
// order plus every event. It only uses t.Errorf, so it may run in a goroutine.
func runAll(t *testing.T, ctx context.Context, c github.Client, o Options, jobs ...Job) ([]model.Result, []Event) {
	t.Helper()
	q := NewQueue(ctx, c, o)
	events := collect(q)
	q.Pause()
	ids := make([]JobID, len(jobs))
	for i, j := range jobs {
		id, err := q.Submit(j.Action, j.PR)
		if err != nil {
			t.Errorf("submit %s %s: %v", j.Action, j.PR.Ref, err)
		}
		ids[i] = id
	}
	q.Close(false)
	evs := <-events
	byID := map[JobID]model.Result{}
	for _, e := range evs {
		if e.Kind == EventFinished {
			byID[e.Job.ID] = e.Job.Result
		}
	}
	out := make([]model.Result, len(ids))
	for i, id := range ids {
		out[i] = byID[id]
	}
	return out, evs
}

func TestApprove(t *testing.T) {
	f := githubtest.NewFake("octocat")
	pending := githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	done := githubtest.NewPR("acme/api", 2, "Bump b from 1.0.0 to 1.0.1")
	done.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: time.Now()}}
	f.Add(pending, true, false)
	f.Add(done, true, false)

	s := &sleeps{}
	got, _ := runAll(t, context.Background(), f, opts(s), job(f, "approve", "acme/api#1"), job(f, "approve", "acme/api#2"))
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
	if diff := cmp.Diff([]time.Duration{DefaultDelay}, s.got()); diff != "" {
		t.Errorf("one cooldown between the two same-repo jobs (-want +got):\n%s", diff)
	}
}

func TestApproveMutationError(t *testing.T) {
	f := githubtest.NewFake("octocat")
	f.Add(githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	f.MutationErr["approve acme/api#1"] = errors.New("Resource not accessible by integration")
	got, _ := runAll(t, context.Background(), f, opts(&sleeps{}), job(f, "approve", "acme/api#1"))
	if got[0].Status != model.ResultFailed || got[0].Reason != model.ReasonMutationFailed || got[0].Message != "Resource not accessible by integration" {
		t.Errorf("got %+v", got[0])
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
	var jobs []Job
	for i, repo := range []string{"acme/a", "acme/b", "acme/c", "acme/d"} {
		f.Add(githubtest.NewPR(repo, i+1, "Bump a from 1.0.0 to 1.0.1"), true, false)
		jobs = append(jobs, job(f, "approve", fmt.Sprintf("%s#%d", repo, i+1)))
	}
	g := &gate{Fake: f, release: make(chan struct{})}
	done := make(chan []model.Result)
	go func() {
		got, _ := runAll(t, context.Background(), g, opts(&sleeps{}), jobs...)
		done <- got
	}()
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
	for range jobs {
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

func TestParentCancelStopsQueuedJobs(t *testing.T) {
	f := githubtest.NewFake("octocat")
	for i := 1; i <= 3; i++ {
		f.Add(githubtest.NewPR("acme/api", i, fmt.Sprintf("Bump p%d from 1.0.0 to 1.0.1", i)), true, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &sleeps{hook: func(time.Duration) { cancel() }} // Ctrl-C arrives during the cooldown after the first job
	got, evs := runAll(t, ctx, f, opts(s), job(f, "approve", "acme/api#1"), job(f, "approve", "acme/api#2"), job(f, "approve", "acme/api#3"))
	want := []string{"acme/api#1 success ", "acme/api#2 skipped cancelled", "acme/api#3 skipped cancelled"}
	if diff := cmp.Diff(want, statuses(got)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if got[1].Message != "cancelled before it started" {
		t.Errorf("message = %q", got[1].Message)
	}
	if len(f.Calls) != 1 {
		t.Errorf("calls after cancel = %v", f.Calls)
	}
	if evs[len(evs)-1].Kind != EventClosed {
		t.Errorf("last event = %v, want Closed", evs[len(evs)-1].Kind)
	}
}

// Deleted in Task 3 together with Run.
func TestRunWrapperKeepsPresetResults(t *testing.T) {
	f := githubtest.NewFake("octocat")
	f.Add(githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	pre := &model.Result{Action: "merge", Ref: "acme/api#9", Status: model.ResultFailed, Reason: model.ReasonNotEligible, Steps: []string{}}
	var seen []string
	o := opts(&sleeps{})
	o.OnResult = func(r model.Result) { seen = append(seen, r.Ref) }
	got := Run(context.Background(), f, []plan.Task{{Action: "merge", Result: pre}, {Action: "approve", PR: enriched(f, "acme/api#1")}}, o)
	if diff := cmp.Diff([]string{"acme/api#9 failed not_eligible", "acme/api#1 success "}, statuses(got)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if len(seen) != 2 {
		t.Errorf("OnResult calls = %v", seen)
	}
}

// cancelDuringApprove cancels the parent context inside the first approval
// and gives the queue time to cancel the queued jobs before returning.
type cancelDuringApprove struct {
	*githubtest.Fake
	cancel context.CancelFunc
}

func (c *cancelDuringApprove) Approve(ctx context.Context, id, head string) error {
	c.cancel()
	time.Sleep(20 * time.Millisecond)
	return c.Fake.Approve(ctx, id, head)
}

// Deleted in Task 3 together with Run.
func TestRunWrapperReportsInTaskOrder(t *testing.T) {
	f := githubtest.NewFake("octocat")
	f.Add(githubtest.NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	f.Add(githubtest.NewPR("acme/api", 2, "Bump b from 1.0.0 to 1.0.1"), true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen []string
	o := opts(&sleeps{})
	o.OnResult = func(r model.Result) { seen = append(seen, r.Ref+" "+r.Status+" "+r.Reason) }
	tasks := []plan.Task{{Action: "approve", PR: enriched(f, "acme/api#1")}, {Action: "approve", PR: enriched(f, "acme/api#2")}}
	Run(ctx, &cancelDuringApprove{Fake: f, cancel: cancel}, tasks, o)
	if diff := cmp.Diff([]string{"acme/api#1 success ", "acme/api#2 skipped cancelled"}, seen); diff != "" {
		t.Errorf("OnResult order (-want +got):\n%s", diff)
	}
}
