// Package executor runs resolved plan tasks against GitHub.
package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/triage"
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

// MergeMethod picks the viewer's default method when the repo allows it,
// otherwise the first allowed of SQUASH, MERGE, REBASE ("" if none).
func MergeMethod(s model.RepoSettings) string {
	allowed := map[string]bool{"MERGE": s.MergeCommitAllowed, "SQUASH": s.SquashMergeAllowed, "REBASE": s.RebaseMergeAllowed}
	if allowed[s.ViewerDefaultMergeMethod] {
		return s.ViewerDefaultMergeMethod
	}
	for _, m := range []string{"SQUASH", "MERGE", "REBASE"} {
		if allowed[m] {
			return m
		}
	}
	return ""
}

func (e *executor) merge(ctx context.Context, t plan.Task) model.Result {
	r := newResult(t)
	pr := t.PR
	if pr.AutoMerge {
		return finish(r, model.ResultSkipped, model.ReasonAlreadyMerging, "auto-merge is already enabled")
	}
	if pr.MergeQueue {
		return finish(r, model.ResultSkipped, model.ReasonMergeQueue, "the base branch uses a merge queue, which is not supported")
	}
	mctx := context.WithoutCancel(ctx)
	cur := pr
	refetch := pr.MergeStateStatus == "UNKNOWN"
	if !pr.ViewerApproved {
		if err := e.c.Approve(mctx, pr.ID, pr.HeadOid); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "approved")
		refetch = true
	}
	if refetch {
		var err error
		if cur, err = e.refetch(ctx, pr); err != nil {
			return finish(r, model.ResultFailed, model.ReasonRefetchFailed, err.Error())
		}
		switch {
		case cur.HeadOid != pr.HeadOid:
			return finish(r, model.ResultFailed, model.ReasonHeadChanged, fmt.Sprintf("head moved from %s to %s", pr.HeadOid, cur.HeadOid))
		case cur.Checks.Failed > 0:
			return finish(r, model.ResultFailed, model.ReasonChecksFailing, fmt.Sprintf("%d failing check(s)", cur.Checks.Failed))
		case cur.AutoMerge:
			return finish(r, model.ResultSkipped, model.ReasonAlreadyMerging, "auto-merge was enabled meanwhile")
		}
	}
	method := MergeMethod(cur.RepoSettings)
	if method == "" {
		return finish(r, model.ResultSkipped, model.ReasonNoMergeMethod, "the repo allows no merge method")
	}
	label := strings.ToLower(method)
	switch {
	case cur.MergeStateStatus == "CLEAN" || cur.MergeStateStatus == "HAS_HOOKS":
		if err := e.c.Merge(mctx, cur.ID, pr.HeadOid, method); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "merged ("+label+")")
	case cur.MergeStateStatus == "DIRTY":
		return finish(r, model.ResultSkipped, model.ReasonNotMergeable, "merge conflicts with "+cur.BaseRef)
	case cur.RepoSettings.AutoMergeAllowed:
		if err := e.c.EnableAutoMerge(mctx, cur.ID, pr.HeadOid, method); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "auto-merge enabled ("+label+")")
	default:
		return finish(r, model.ResultSkipped, model.ReasonNotMergeable,
			fmt.Sprintf("merge state is %s and auto-merge is not allowed in this repo", cur.MergeStateStatus))
	}
	return finish(r, model.ResultSuccess, "", "")
}

// refetch reloads one PR, polling while GitHub still computes mergeability.
func (e *executor) refetch(ctx context.Context, pr *model.PR) (*model.PR, error) {
	for attempt := 0; ; attempt++ {
		prs, warns, err := e.c.FetchPRs(ctx, e.o.Viewer, []model.PRRef{pr.PRRef()})
		if err != nil {
			return nil, err
		}
		if len(prs) == 0 {
			msg := "pull request not returned"
			if len(warns) > 0 {
				msg = warns[0].Message
			}
			return nil, errors.New(msg)
		}
		cur := prs[0]
		triage.Enrich(cur)
		if cur.MergeStateStatus != "UNKNOWN" || attempt >= unknownPolls {
			return cur, nil
		}
		if err := e.o.Sleep(ctx, e.o.PollInterval); err != nil {
			return cur, nil
		}
	}
}
