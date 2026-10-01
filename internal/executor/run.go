package executor

import (
	"context"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
)

// Run executes tasks through a Queue and returns one result per task in
// task order, calling o.OnResult for each, also in task order (a cancelled
// queued job may finish before the running one ahead of it). It only exists
// until the CLI and the TUI submit to a Queue directly.
func Run(ctx context.Context, c github.Client, tasks []plan.Task, o Options) []model.Result {
	onResult := o.OnResult
	if onResult == nil {
		onResult = func(model.Result) {}
	}
	results := make([]model.Result, len(tasks))
	finished := make([]bool, len(tasks))
	next := 0
	report := func(i int, r model.Result) {
		results[i], finished[i] = r, true
		for ; next < len(tasks) && finished[next]; next++ {
			onResult(results[next])
		}
	}
	q := NewQueue(ctx, c, o)
	q.Pause()
	idx := map[JobID]int{}
	for i, t := range tasks {
		if t.Result != nil {
			report(i, *t.Result)
			continue
		}
		id, err := q.Submit(t.Action, t.PR)
		if err != nil {
			report(i, cancelledResult(t.Action, t.PR))
			continue
		}
		idx[id] = i
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range q.Events() {
			if i, ok := idx[e.Job.ID]; ok && e.Kind == EventFinished {
				report(i, e.Job.Result)
			}
		}
	}()
	q.Close(false)
	<-done
	return results
}
