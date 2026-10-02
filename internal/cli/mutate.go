package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/policy"
)

type mutateOpts struct {
	yes        bool
	allowMajor bool
}

func (a *app) mutateCmd(action, short string) *cobra.Command {
	var mo mutateOpts
	cmd := &cobra.Command{
		Use:   action + " <refs...>",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.emit(output{command: action, dryRun: !mo.yes},
					&usageError{msg: action + " takes at least one ref (owner/repo#123 or group:<package>@<target>)"})
			}
			return a.runPlan(cmd, action, plan.Items(action, args), mo)
		},
	}
	cmd.Flags().BoolVar(&mo.yes, "yes", false, "execute the plan (default is a dry run)")
	cmd.Flags().BoolVar(&mo.allowMajor, "allow-major", false, "allow major version bumps")
	return cmd
}

// runPlan loads a fresh snapshot, resolves items, applies policy and either
// reports the plan (dry run) or executes the allowed tasks.
func (a *app) runPlan(cmd *cobra.Command, name string, items []plan.Item, mo mutateOpts) error {
	out := output{command: name, dryRun: !mo.yes}
	if err := a.prepare(cmd); err != nil {
		return a.emit(out, err)
	}
	client, snap, err := a.loadSnapshot(cmd.Context())
	if err != nil {
		return a.emit(out, err)
	}
	out.viewer, out.warnings = snap.Viewer, snap.Warnings
	tasks, err := plan.Resolve(snap, items)
	if err != nil {
		return a.emit(out, err)
	}
	rules := policy.Rules{Bots: a.cfg.Bots, Soft: true, AllowMajor: mo.allowMajor || a.cfg.Policy.AllowMajor,
		AllowRepos: a.cfg.Policy.Repos.Allow, DenyRepos: a.cfg.Policy.Repos.Deny}

	results := make([]model.Result, len(tasks))
	var run []plan.Task
	var runIdx []int
	for i, t := range tasks {
		if t.Result != nil {
			results[i] = *t.Result
			continue
		}
		r := model.Result{Action: t.Action, Ref: t.PR.Ref, HeadOid: t.PR.HeadOid, Steps: []string{}}
		if v := policy.Evaluate(t.Action, t.PR, rules); !v.Allow {
			r.Status, r.Reason, r.Message = model.ResultDenied, v.Reason, v.Message
			results[i] = r
			continue
		}
		if !mo.yes {
			r.Status = model.ResultPlanned
			results[i] = r
			continue
		}
		run = append(run, t)
		runIdx = append(runIdx, i)
	}
	if len(run) > 0 {
		got := a.runQueue(cmd.Context(), client, snap.Viewer, run)
		for k, i := range runIdx {
			results[i] = got[k]
		}
	}
	out.data = resultsData{Results: results, Counts: countResults(results)}
	out.code = exitCodeFor(results, !mo.yes)
	out.human = func(w io.Writer) { writeResults(w, results, !mo.yes) }
	return a.emit(out, nil)
}

// runQueue submits tasks to a fresh queue, prints a progress line for every
// finished job and returns the results in task order. Jobs are queued while
// the queue is paused so they start in task order; Close(false) resumes it
// and returns once every job has run (or, after Ctrl-C, been cancelled).
func (a *app) runQueue(ctx context.Context, client github.Client, viewer string, tasks []plan.Task) []model.Result {
	q := executor.NewQueue(ctx, client, executor.Options{Viewer: viewer, Rules: policy.Rules{Bots: a.cfg.Bots}, Sleep: a.sleep})
	results := make([]model.Result, len(tasks))
	idx := map[executor.JobID]int{}
	q.Pause()
	for i, t := range tasks {
		id, err := q.Submit(t.Action, t.PR)
		if err != nil { // Submit failed: the queue closed after Ctrl-C
			results[i] = executor.CancelledResult(t.Action, t.PR)
			a.progressResult(results[i])
			continue
		}
		idx[id] = i
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range q.Events() {
			if i, ok := idx[ev.Job.ID]; ok && ev.Kind == executor.EventFinished {
				results[i] = ev.Job.Result
				a.progressResult(ev.Job.Result)
			}
		}
	}()
	q.Close(false)
	<-done
	return results
}

func (a *app) progressResult(r model.Result) {
	fmt.Fprintf(a.stderr, "%s %s %s: %s\n", r.Status, r.Action, sanitize(r.Ref), sanitize(dash(resultDetail(r))))
}
