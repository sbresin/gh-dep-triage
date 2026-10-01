// Package executor runs approve and merge jobs against GitHub through a
// long-lived Queue shared by the CLI and the TUI.
package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
)

const (
	DefaultRepoParallel = 2
	DefaultDelay        = 2 * time.Second
	DefaultPollInterval = time.Second
	mutationTimeout     = 30 * time.Second
	unknownPolls        = 3
	// stepBacklog is how many undelivered events may pile up before Step
	// events are dropped. Other kinds are never dropped.
	stepBacklog = 256
)

// Options configures a Queue. Zero values get the defaults above.
type Options struct {
	Viewer string
	// Rules are re-checked for every job right before it runs. Only the hard
	// rules apply; Soft is ignored.
	Rules        policy.Rules
	RepoParallel int
	Delay        time.Duration
	PollInterval time.Duration
	// MutationTimeout bounds each mutation call; it is not cancelled by ctx.
	MutationTimeout time.Duration
	// Sleep waits for d or until ctx is done. It also enforces the per-repo
	// cooldown, so tests replace it to run without waiting.
	Sleep func(context.Context, time.Duration) error
}

func (o Options) withDefaults() Options {
	if o.RepoParallel <= 0 {
		o.RepoParallel = DefaultRepoParallel
	}
	if o.Delay == 0 {
		o.Delay = DefaultDelay
	}
	if o.PollInterval == 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.MutationTimeout <= 0 {
		o.MutationTimeout = mutationTimeout
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return o
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type JobID int

type JobState int

const (
	JobQueued JobState = iota
	JobRunning
	JobDone
	JobCancelled
)

func (s JobState) String() string {
	switch s {
	case JobQueued:
		return "queued"
	case JobRunning:
		return "running"
	case JobDone:
		return "done"
	default:
		return "cancelled"
	}
}

// Job is one (action, PR) pair. PR is the PR as the user confirmed it, and
// PR.HeadOid is the pinned head. Step is the latest step while running.
type Job struct {
	ID     JobID
	Action string
	PR     *model.PR
	State  JobState
	Step   string
	Result model.Result
}

// Finished reports whether the job is done or was cancelled.
func (j Job) Finished() bool { return j.State == JobDone || j.State == JobCancelled }

type EventKind int

const (
	EventQueued EventKind = iota
	EventStarted
	EventStep
	EventFinished
	EventIdle
	EventClosed
)

// Event reports one queue change. Job is a copy; it is the zero Job for
// Idle and Closed.
type Event struct {
	Kind EventKind
	Job  Job
}

var (
	ErrAlreadyQueued  = errors.New("already queued")
	ErrClosed         = errors.New("the queue is closed")
	ErrNotCancellable = errors.New("only queued jobs can be cancelled")
	ErrUnknownJob     = errors.New("no such job")
)

// Queue runs jobs serially per repo, at most RepoParallel repos at once,
// with a cooldown of Delay between jobs of one repo. A single scheduler
// goroutine owns all state; public methods hand it closures.
type Queue struct {
	c      github.Client
	o      Options
	ctx    context.Context
	cancel context.CancelFunc
	reqs   chan func(*state)
	done   chan struct{}
	final  []Job // written by the scheduler before done is closed
	events chan Event
	pump   *pump
}

type state struct {
	jobs    []*Job
	nextID  JobID
	paused  bool
	closing bool
	idle    bool
	busy    map[string]bool // repos with a running job
	cool    map[string]bool // repos waiting out Delay after a job
	exit    bool
}

func repoKey(pr *model.PR) string { return strings.ToLower(pr.Repo) }

func jobKey(action string, pr *model.PR) string { return action + " " + strings.ToLower(pr.Ref) }

func (s *state) find(id JobID) *Job {
	for _, j := range s.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (s *state) active() bool {
	for _, j := range s.jobs {
		if !j.Finished() {
			return true
		}
	}
	return false
}

func (s *state) queuedIn(repo string) bool {
	for _, j := range s.jobs {
		if j.State == JobQueued && repoKey(j.PR) == repo {
			return true
		}
	}
	return false
}

func (s *state) snapshot() []Job {
	out := make([]Job, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = *j
	}
	return out
}

// NewQueue starts a queue. Cancelling ctx acts like Close(true).
func NewQueue(ctx context.Context, c github.Client, o Options) *Queue {
	qctx, cancel := context.WithCancel(ctx)
	q := &Queue{
		c: c, o: o.withDefaults(), ctx: qctx, cancel: cancel,
		reqs: make(chan func(*state)), done: make(chan struct{}),
		events: make(chan Event), pump: &pump{wake: make(chan struct{}, 1)},
	}
	go q.pump.run(q.events)
	go q.loop()
	go func() {
		select {
		case <-ctx.Done():
			q.Close(true)
		case <-q.done:
		}
	}()
	return q
}

func (q *Queue) loop() {
	s := &state{nextID: 1, idle: true, busy: map[string]bool{}, cool: map[string]bool{}}
	for !s.exit {
		f := <-q.reqs
		f(s)
	}
	q.final = s.snapshot()
	q.cancel()
	close(q.done)
}

// do runs f on the scheduler goroutine and waits for it. It returns false
// once the queue has shut down.
func (q *Queue) do(f func(*state)) bool {
	ack := make(chan struct{})
	select {
	case q.reqs <- func(s *state) { f(s); close(ack) }:
		<-ack
		return true
	case <-q.done:
		return false
	}
}

func (q *Queue) emit(kind EventKind, j *Job) {
	e := Event{Kind: kind}
	if j != nil {
		e.Job = *j
	}
	q.pump.push(e)
}

// Submit queues action for pr. It fails with ErrAlreadyQueued while the same
// action for the same PR is queued or running, and with ErrClosed once the
// queue is closing.
func (q *Queue) Submit(action string, pr *model.PR) (JobID, error) {
	var id JobID
	err := ErrClosed
	q.do(func(s *state) {
		if s.closing {
			return
		}
		key := jobKey(action, pr)
		for _, j := range s.jobs {
			if !j.Finished() && jobKey(j.Action, j.PR) == key {
				err = ErrAlreadyQueued
				return
			}
		}
		j := &Job{ID: s.nextID, Action: action, PR: pr, State: JobQueued}
		s.nextID++
		s.jobs = append(s.jobs, j)
		q.emit(EventQueued, j)
		q.schedule(s)
		q.settle(s)
		id, err = j.ID, nil
	})
	return id, err
}

// Cancel cancels a queued job.
func (q *Queue) Cancel(id JobID) error {
	err := ErrClosed
	q.do(func(s *state) {
		j := s.find(id)
		switch {
		case j == nil:
			err = ErrUnknownJob
		case j.State != JobQueued:
			err = ErrNotCancellable
		default:
			q.cancelJob(j)
			q.settle(s)
			err = nil
		}
	})
	return err
}

func (q *Queue) cancelJob(j *Job) {
	j.State, j.Step, j.Result = JobCancelled, "", cancelledResult(j.Action, j.PR)
	q.emit(EventFinished, j)
}

// Pause stops starting new jobs; running jobs finish.
func (q *Queue) Pause() { q.do(func(s *state) { s.paused = true }) }

func (q *Queue) Resume() {
	q.do(func(s *state) {
		s.paused = false
		q.schedule(s)
	})
}

// ClearFinished drops done and cancelled jobs from Jobs.
func (q *Queue) ClearFinished() {
	q.do(func(s *state) {
		kept := s.jobs[:0]
		for _, j := range s.jobs {
			if !j.Finished() {
				kept = append(kept, j)
			}
		}
		s.jobs = kept
	})
}

// Jobs returns copies of all jobs in submission order. After the queue has
// closed it returns the final state.
func (q *Queue) Jobs() []Job {
	var out []Job
	if !q.do(func(s *state) { out = s.snapshot() }) {
		return append([]Job(nil), q.final...)
	}
	return out
}

// Events delivers queue events in order. It must be drained; it is closed
// after EventClosed.
func (q *Queue) Events() <-chan Event { return q.events }

// Close stops accepting jobs. With cancelQueued, queued jobs are cancelled
// and running ones are asked to stop before their next mutation; otherwise
// every queued job still runs. Close returns once nothing is running; it is
// safe to call more than once.
func (q *Queue) Close(cancelQueued bool) {
	q.do(func(s *state) {
		s.closing = true
		if cancelQueued {
			q.cancel()
			for _, j := range s.jobs {
				if j.State == JobQueued {
					q.cancelJob(j)
				}
			}
		} else {
			s.paused = false
			q.schedule(s)
		}
		q.settle(s)
	})
	<-q.done
}

// schedule starts queued jobs in submission order, skipping repos that are
// busy or cooling down, while fewer than RepoParallel repos are busy.
func (q *Queue) schedule(s *state) {
	if s.paused || q.ctx.Err() != nil {
		return
	}
	for _, j := range s.jobs {
		if len(s.busy) >= q.o.RepoParallel {
			return
		}
		repo := repoKey(j.PR)
		if j.State != JobQueued || s.busy[repo] || s.cool[repo] {
			continue
		}
		s.busy[repo] = true
		j.State, j.Step = JobRunning, "starting"
		q.emit(EventStarted, j)
		go q.work(*j)
	}
}

func (q *Queue) work(j Job) {
	res := q.run(q.ctx, j, func(step string) {
		q.do(func(s *state) {
			if p := s.find(j.ID); p != nil && p.State == JobRunning {
				p.Step = step
				q.emit(EventStep, p)
			}
		})
	})
	q.do(func(s *state) {
		repo := repoKey(j.PR)
		delete(s.busy, repo)
		if p := s.find(j.ID); p != nil {
			p.State, p.Step, p.Result = JobDone, "", res
			q.emit(EventFinished, p)
		}
		if !s.closing || s.queuedIn(repo) {
			s.cool[repo] = true
			go q.cooldown(repo)
		}
		q.schedule(s)
		q.settle(s)
	})
}

func (q *Queue) cooldown(repo string) {
	_ = q.o.Sleep(q.ctx, q.o.Delay)
	q.do(func(s *state) {
		delete(s.cool, repo)
		q.schedule(s)
		q.settle(s)
	})
}

// settle emits Idle when the last active job finished and shuts the queue
// down once it is closing and nothing is left.
func (q *Queue) settle(s *state) {
	active := s.active()
	if active {
		s.idle = false
	} else if !s.idle {
		s.idle = true
		q.emit(EventIdle, nil)
	}
	if s.closing && !active && len(s.busy) == 0 && !s.exit {
		q.emit(EventClosed, nil)
		q.pump.close()
		s.exit = true
	}
}

// pump delivers events in order without ever blocking the sender. When the
// consumer falls behind, Step events are dropped; nothing else is.
type pump struct {
	mu     sync.Mutex
	buf    []Event
	closed bool
	wake   chan struct{}
}

func (p *pump) push(e Event) {
	p.mu.Lock()
	if e.Kind == EventStep && len(p.buf) >= stepBacklog {
		p.mu.Unlock()
		return
	}
	p.buf = append(p.buf, e)
	p.mu.Unlock()
	p.signal()
}

func (p *pump) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.signal()
}

func (p *pump) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *pump) run(out chan<- Event) {
	for {
		p.mu.Lock()
		if len(p.buf) == 0 {
			closed := p.closed
			p.mu.Unlock()
			if closed {
				close(out)
				return
			}
			<-p.wake
			continue
		}
		e := p.buf[0]
		p.buf = p.buf[1:]
		p.mu.Unlock()
		out <- e
	}
}
