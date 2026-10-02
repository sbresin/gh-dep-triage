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

func repoFake(refs ...string) *githubtest.Fake {
	f := githubtest.NewFake("octocat")
	for i, ref := range refs {
		repo := ref[:strings.Index(ref, "#")]
		f.Add(githubtest.NewPR(repo, i+1, "Bump a from 1.0.0 to 1.0.1"), true, false)
	}
	return f
}

func TestPreRunCheck(t *testing.T) {
	tests := []struct {
		name, action string
		mod          func(f *githubtest.Fake)
		want, msg    string
	}{
		{"refetch fails", "approve", func(f *githubtest.Fake) { f.FailBatch["acme/api#1"] = errors.New("HTTP 502") },
			"failed refetch_failed", "HTTP 502"},
		{"merged meanwhile", "approve", func(f *githubtest.Fake) { f.PRs["acme/api#1"].State = "MERGED" },
			"skipped not_open", "pull request is merged"},
		{"head moved", "merge", func(f *githubtest.Fake) { f.PRs["acme/api#1"].HeadOid = "sha-new" },
			"failed head_changed", "head moved from sha1 to sha-new since you confirmed"},
		{"author no longer a bot", "approve", func(f *githubtest.Fake) { f.PRs["acme/api#1"].Author = "mallory" },
			"denied not_bot_pr", "mallory"},
		{"checks now failing", "merge", func(f *githubtest.Fake) {
			f.PRs["acme/api#1"].CheckRuns = []model.Check{githubtest.CheckRun("test", "COMPLETED", "FAILURE", 1)}
		}, "denied checks_failing", "1 failing check(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := repoFake("acme/api#1")
			j := job(f, tt.action, "acme/api#1") // what the user confirmed
			tt.mod(f)
			got, _ := runAll(t, context.Background(), f, opts(&sleeps{}), j)
			if st := got[0].Status + " " + got[0].Reason; st != tt.want || !strings.Contains(got[0].Message, tt.msg) {
				t.Errorf("got %+v, want %q with message containing %q", got[0], tt.want, tt.msg)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no mutation may run: %v", f.Calls)
			}
		})
	}
}

func TestCooldownOnlyBetweenSameRepoJobs(t *testing.T) {
	f := repoFake("acme/api#1", "acme/web#2", "acme/api#3")
	s := &sleeps{}
	got, _ := runAll(t, context.Background(), f, opts(s),
		job(f, "approve", "acme/api#1"), job(f, "approve", "acme/web#2"), job(f, "approve", "acme/api#3"))
	for _, r := range got {
		if r.Status != model.ResultSuccess {
			t.Errorf("%+v", r)
		}
	}
	if diff := cmp.Diff([]time.Duration{DefaultDelay}, s.got()); diff != "" {
		t.Errorf("sleeps (-want +got):\n%s", diff)
	}
}

func TestFIFOSkipsCoolingRepo(t *testing.T) {
	f := repoFake("acme/a#1", "acme/a#2", "acme/b#3")
	o := opts(&sleeps{})
	o.RepoParallel = 1
	_, evs := runAll(t, context.Background(), f, o,
		job(f, "approve", "acme/a#1"), job(f, "approve", "acme/a#2"), job(f, "approve", "acme/b#3"))
	started := []string{}
	for _, e := range evs {
		if e.Kind == EventStarted {
			started = append(started, e.Job.PR.Ref)
		}
	}
	if diff := cmp.Diff([]string{"acme/a#1", "acme/b#3", "acme/a#2"}, started); diff != "" {
		t.Errorf("start order (-want +got):\n%s", diff)
	}
}

func TestEventsPerJobInOrder(t *testing.T) {
	f := repoFake("acme/api#1")
	_, evs := runAll(t, context.Background(), f, opts(&sleeps{}), job(f, "approve", "acme/api#1"))
	kinds, steps := []EventKind{}, []string{}
	for _, e := range evs {
		if e.Kind == EventStep {
			steps = append(steps, e.Job.Step)
			continue
		}
		kinds = append(kinds, e.Kind)
	}
	if diff := cmp.Diff([]EventKind{EventQueued, EventStarted, EventFinished, EventIdle, EventClosed}, kinds); diff != "" {
		t.Errorf("kinds (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"checking", "approving"}, steps); diff != "" {
		t.Errorf("steps (-want +got):\n%s", diff)
	}
}

func TestPauseResume(t *testing.T) {
	f := repoFake("acme/api#1")
	q := NewQueue(context.Background(), f, opts(&sleeps{}))
	q.Pause()
	if _, err := q.Submit("approve", enriched(f, "acme/api#1")); err != nil {
		t.Fatal(err)
	}
	if jobs := q.Jobs(); len(jobs) != 1 || jobs[0].State != JobQueued || len(f.Calls) != 0 {
		t.Fatalf("paused queue must not start: %+v calls=%v", jobs, f.Calls)
	}
	q.Resume()
	for e := range q.Events() {
		if e.Kind == EventFinished {
			if e.Job.Result.Status != model.ResultSuccess {
				t.Errorf("result %+v", e.Job.Result)
			}
			break
		}
	}
	q.Close(false)
	for range q.Events() {
	}
}

func TestCancelOnlyQueued(t *testing.T) {
	f := repoFake("acme/api#1", "acme/api#2")
	q := NewQueue(context.Background(), f, opts(&sleeps{}))
	events := collect(q)
	q.Pause()
	id1, _ := q.Submit("approve", enriched(f, "acme/api#1"))
	id2, _ := q.Submit("approve", enriched(f, "acme/api#2"))
	if err := q.Cancel(id2); err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	if err := q.Cancel(id2); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancel cancelled: err = %v", err)
	}
	if err := q.Cancel(99); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("cancel unknown: err = %v", err)
	}
	q.Close(false)
	byID := map[JobID]model.Result{}
	for _, e := range <-events {
		if e.Kind == EventFinished {
			byID[e.Job.ID] = e.Job.Result
		}
	}
	if byID[id1].Status != model.ResultSuccess || byID[id2].Reason != model.ReasonCancelled || byID[id2].Message != "cancelled before it started" {
		t.Errorf("results %+v", byID)
	}
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestDuplicatesRetryAndClosed(t *testing.T) {
	f := repoFake("acme/api#1")
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	q := NewQueue(context.Background(), f, opts(&sleeps{}))
	q.Pause()
	pr := enriched(f, "acme/api#1")
	if _, err := q.Submit("merge", pr); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Submit("merge", pr); !errors.Is(err, ErrAlreadyQueued) {
		t.Errorf("duplicate merge: err = %v", err)
	}
	if _, err := q.Submit("approve", pr); err != nil {
		t.Errorf("approve of the same PR is a different job: %v", err)
	}
	q.Resume()
	finished := 0
	for e := range q.Events() {
		if e.Kind == EventFinished {
			if finished++; finished == 2 {
				break
			}
		}
	}
	if _, err := q.Submit("merge", pr); err != nil {
		t.Errorf("a finished job may be submitted again (retry): %v", err)
	}
	q.Close(false)
	if _, err := q.Submit("merge", pr); !errors.Is(err, ErrClosed) {
		t.Errorf("submit after close: err = %v", err)
	}
	for range q.Events() {
	}
}

func TestCloseCancelsQueued(t *testing.T) {
	f := repoFake("acme/api#1", "acme/web#2")
	q := NewQueue(context.Background(), f, opts(&sleeps{}))
	events := collect(q)
	q.Pause()
	_, _ = q.Submit("approve", enriched(f, "acme/api#1"))
	_, _ = q.Submit("approve", enriched(f, "acme/web#2"))
	q.Close(true)
	evs := <-events
	kinds := []EventKind{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Kind == EventFinished && (e.Job.State != JobCancelled || e.Job.Result.Reason != model.ReasonCancelled) {
			t.Errorf("finished %+v", e.Job)
		}
	}
	want := []EventKind{EventQueued, EventQueued, EventFinished, EventFinished, EventIdle, EventClosed}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Errorf("kinds (-want +got):\n%s", diff)
	}
	if len(f.Calls) != 0 {
		t.Errorf("calls %v", f.Calls)
	}
}

// Stop cancels at once, without waiting for the running job.
func TestStopDoesNotWait(t *testing.T) {
	f := repoFake("acme/api#1", "acme/api#2")
	g := &gate{Fake: f, release: make(chan struct{})}
	q := NewQueue(context.Background(), g, opts(&sleeps{}))
	events := collect(q)
	_, _ = q.Submit("approve", enriched(f, "acme/api#1"))
	_, _ = q.Submit("approve", enriched(f, "acme/api#2"))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		g.mu.Lock()
		n := g.inFlight
		g.mu.Unlock()
		if n == 1 {
			break
		}
	}
	q.Stop(true)
	if jobs := q.Jobs(); jobs[0].State != JobRunning || jobs[1].State != JobCancelled {
		t.Errorf("after Stop: %v, %v", jobs[0].State, jobs[1].State)
	}
	if _, err := q.Submit("merge", enriched(f, "acme/api#1")); !errors.Is(err, ErrClosed) {
		t.Errorf("Submit after Stop: %v", err)
	}
	close(g.release)
	if evs := <-events; evs[len(evs)-1].Kind != EventClosed {
		t.Errorf("last event %v", evs[len(evs)-1].Kind)
	}
}

func TestParentCancelClosesQueue(t *testing.T) {
	f := repoFake("acme/api#1")
	ctx, cancel := context.WithCancel(context.Background())
	q := NewQueue(ctx, f, opts(&sleeps{}))
	events := collect(q)
	q.Pause()
	_, _ = q.Submit("approve", enriched(f, "acme/api#1"))
	cancel()
	select {
	case evs := <-events:
		if last := evs[len(evs)-1]; last.Kind != EventClosed {
			t.Errorf("last event %v", last.Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the parent context did not close the queue")
	}
	q.Close(false) // idempotent, returns at once
	if jobs := q.Jobs(); len(jobs) != 1 || jobs[0].State != JobCancelled {
		t.Errorf("final jobs %+v", jobs)
	}
}

func TestClearFinished(t *testing.T) {
	f := repoFake("acme/api#1", "acme/api#2")
	q := NewQueue(context.Background(), f, opts(&sleeps{}))
	q.Pause()
	id1, _ := q.Submit("approve", enriched(f, "acme/api#1"))
	id2, _ := q.Submit("approve", enriched(f, "acme/api#2"))
	_ = q.Cancel(id1)
	q.ClearFinished()
	if jobs := q.Jobs(); len(jobs) != 1 || jobs[0].ID != id2 {
		t.Errorf("jobs after clear %+v", jobs)
	}
	q.Close(true)
	for range q.Events() {
	}
}

func TestPumpDropsOnlySteps(t *testing.T) {
	p := &pump{wake: make(chan struct{}, 1)}
	for range stepBacklog + 50 {
		p.push(Event{Kind: EventStep})
	}
	p.push(Event{Kind: EventFinished})
	p.push(Event{Kind: EventIdle})
	p.push(Event{Kind: EventClosed})
	p.close()
	out := make(chan Event)
	go p.run(out)
	steps, tail := 0, []EventKind{}
	for e := range out {
		if e.Kind == EventStep {
			steps++
		} else {
			tail = append(tail, e.Kind)
		}
	}
	if steps != stepBacklog {
		t.Errorf("steps delivered = %d, want %d", steps, stepBacklog)
	}
	if diff := cmp.Diff([]EventKind{EventFinished, EventIdle, EventClosed}, tail); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

// A Stop(true) that lands right after a step is reported stops the job
// before the mutation that step announces.
func TestStopAtStepBoundarySkipsMutation(t *testing.T) {
	clean := func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	pending := func(p *model.PR) {
		p.CheckRuns = []model.Check{githubtest.CheckRun("test", "IN_PROGRESS", "", 1)}
	}
	tests := []struct {
		action, at, msg string
		mods            []func(*model.PR)
	}{
		{"approve", "approving", "cancelled before approving", nil},
		{"merge", "approving", "cancelled before approving", nil},
		{"merge", "merging", "cancelled before merging", []func(*model.PR){approved, clean}},
		{"merge", "enabling auto-merge", "cancelled before merging", []func(*model.PR){approved, pending}},
	}
	for _, tt := range tests {
		t.Run(tt.action+" "+tt.at, func(t *testing.T) {
			f := newMergeFake(tt.mods...)
			q := NewQueue(context.Background(), f, opts(&sleeps{}))
			stepped := false
			r := q.run(q.ctx, job(f, tt.action, "acme/api#1"), func(s string) {
				if s == tt.at {
					stepped = true
					q.Stop(true)
				}
			})
			if !stepped || r.Status != model.ResultSkipped || r.Reason != model.ReasonCancelled || r.Message != tt.msg {
				t.Errorf("stepped=%v result %+v", stepped, r)
			}
			if len(f.Calls) != 0 {
				t.Errorf("no mutation may run after Stop: %v", f.Calls)
			}
			for range q.Events() {
			}
		})
	}
}
