// Package executor runs resolved plan tasks against GitHub.
package executor

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
)

const (
	DefaultRepoParallel = 2
	DefaultDelay        = 2 * time.Second
	DefaultPollInterval = time.Second
	unknownPolls        = 3
)

type Options struct {
	Viewer       string
	RepoParallel int
	Delay        time.Duration
	PollInterval time.Duration
	Sleep        func(context.Context, time.Duration) error
	OnResult     func(model.Result)
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
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.OnResult == nil {
		o.OnResult = func(model.Result) {}
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

type executor struct {
	c github.Client
	o Options
}

// Run executes tasks serially per repo, at most RepoParallel repos at once,
// and returns one result per task in task order.
func Run(ctx context.Context, c github.Client, tasks []plan.Task, o Options) []model.Result {
	o = o.withDefaults()
	e := &executor{c: c, o: o}
	results := make([]model.Result, len(tasks))
	var mu sync.Mutex
	emit := func(i int, r model.Result) {
		mu.Lock()
		defer mu.Unlock()
		results[i] = r
		o.OnResult(r)
	}

	var repos []string
	byRepo := map[string][]int{}
	for i, t := range tasks {
		if t.Result != nil {
			emit(i, *t.Result)
			continue
		}
		repo := strings.ToLower(t.PR.Repo)
		if _, ok := byRepo[repo]; !ok {
			repos = append(repos, repo)
		}
		byRepo[repo] = append(byRepo[repo], i)
	}

	queue := make(chan []int, len(repos))
	for _, r := range repos {
		queue <- byRepo[r]
	}
	close(queue)
	var wg sync.WaitGroup
	for range min(o.RepoParallel, len(repos)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range queue {
				for k, i := range idx {
					if k > 0 && ctx.Err() == nil {
						_ = o.Sleep(ctx, o.Delay)
					}
					if ctx.Err() != nil {
						emit(i, cancelled(tasks[i]))
						continue
					}
					emit(i, e.run(ctx, tasks[i]))
				}
			}
		}()
	}
	wg.Wait()
	return results
}

func newResult(t plan.Task) model.Result {
	return model.Result{Action: t.Action, Ref: t.PR.Ref, HeadOid: t.PR.HeadOid, Steps: []string{}}
}

func finish(r model.Result, status, reason, message string) model.Result {
	r.Status, r.Reason, r.Message = status, reason, message
	return r
}

func cancelled(t plan.Task) model.Result {
	return finish(newResult(t), model.ResultSkipped, model.ReasonCancelled, "cancelled before it started")
}

func (e *executor) run(ctx context.Context, t plan.Task) model.Result {
	switch t.Action {
	case model.ActionApprove:
		return e.approve(ctx, t)
	case model.ActionMerge:
		return e.merge(ctx, t)
	default:
		return finish(newResult(t), model.ResultFailed, model.ReasonMutationFailed, "unsupported action "+t.Action)
	}
}

func (e *executor) approve(ctx context.Context, t plan.Task) model.Result {
	r := newResult(t)
	if t.PR.ViewerApproved {
		return finish(r, model.ResultSkipped, model.ReasonAlreadyApproved, "")
	}
	if err := e.c.Approve(context.WithoutCancel(ctx), t.PR.ID, t.PR.HeadOid); err != nil {
		return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
	}
	r.Steps = append(r.Steps, "approved")
	return finish(r, model.ResultSuccess, "", "")
}

// merge is implemented in Task 7.
func (e *executor) merge(_ context.Context, t plan.Task) model.Result {
	return finish(newResult(t), model.ResultFailed, model.ReasonMutationFailed, "merge not implemented")
}
