package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

func newResult(action string, pr *model.PR, args map[string]string) model.Result {
	return model.Result{Action: action, Ref: pr.Ref, Args: args, HeadOid: pr.HeadOid, Steps: []string{}}
}

func finish(r model.Result, status, reason, message string) model.Result {
	r.Status, r.Reason, r.Message = status, reason, message
	return r
}

// CancelledResult is the result of a job cancelled before it started.
func CancelledResult(action string, pr *model.PR, args map[string]string) model.Result {
	return finish(newResult(action, pr, args), model.ResultSkipped, model.ReasonCancelled, "cancelled before it started")
}

// run executes one job: refetch the PR, check it is still open, at the
// pinned head (j.PR.HeadOid) and allowed by the hard rules, then act on the
// fresh data.
func (q *Queue) run(ctx context.Context, j Job, step func(string)) model.Result {
	r := newResult(j.Action, j.PR, j.Args)
	if ctx.Err() != nil {
		return CancelledResult(j.Action, j.PR, j.Args)
	}
	step("checking")
	fresh, err := q.fetch(ctx, j.PR.PRRef())
	switch {
	case ctx.Err() != nil:
		return CancelledResult(j.Action, j.PR, j.Args)
	case err != nil:
		return finish(r, model.ResultFailed, model.ReasonRefetchFailed, err.Error())
	case fresh.State != "OPEN":
		return finish(r, model.ResultSkipped, model.ReasonNotOpen, "pull request is "+strings.ToLower(fresh.State))
	case fresh.HeadOid != j.PR.HeadOid:
		return finish(r, model.ResultFailed, model.ReasonHeadChanged,
			fmt.Sprintf("head moved from %s to %s since you confirmed", j.PR.HeadOid, fresh.HeadOid))
	}
	// Blockers and risk come from the whole snapshot, so a refetched PR has
	// neither; the confirmed PR's still hold at the pinned head.
	fresh.Blockers, fresh.Risk = j.PR.Blockers, j.PR.Risk
	if v := policy.Evaluate(j.Action, fresh, j.Args, policy.Rules{Bots: q.o.Rules.Bots}); !v.Allow {
		return finish(r, model.ResultDenied, v.Reason, v.Message)
	}
	switch j.Action {
	case model.ActionApprove:
		return q.approve(ctx, r, fresh, step)
	case model.ActionMerge:
		return q.merge(ctx, r, fresh, step)
	case model.ActionRebase, model.ActionRecreate:
		return q.botRequest(ctx, r, fresh, j.Action, step)
	case model.ActionRerun:
		return q.rerun(ctx, r, fresh, step)
	case model.ActionRequestReview:
		return q.requestReview(ctx, r, fresh, j.Args["reviewer"], step)
	case model.ActionClose:
		return q.closePR(ctx, r, fresh, j.Args["reason"], step)
	default:
		return finish(r, model.ResultFailed, model.ReasonMutationFailed, "unsupported action "+j.Action)
	}
}

// mutate runs one mutation that finishes even when ctx is cancelled (so an
// in-flight request is never abandoned) but is bounded by MutationTimeout.
func (q *Queue) mutate(ctx context.Context, fn func(context.Context) error) error {
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), q.o.MutationTimeout)
	defer cancel()
	return fn(mctx)
}

func (q *Queue) approve(ctx context.Context, r model.Result, pr *model.PR, step func(string)) model.Result {
	if done, stop := approveCheck(r, pr); stop {
		return done
	}
	step("approving")
	if ctx.Err() != nil {
		return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before approving")
	}
	if err := q.mutate(ctx, func(c context.Context) error { return q.c.Approve(c, pr.ID, pr.HeadOid) }); err != nil {
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

func (q *Queue) merge(ctx context.Context, r model.Result, pr *model.PR, step func(string)) model.Result {
	// Don't approve when the merge would certainly be skipped.
	if done, stop := mergeCheck(r, pr); stop {
		return done
	}
	cur := pr
	refetch := pr.MergeStateStatus == "UNKNOWN"
	if !pr.ViewerApproved {
		step("approving")
		if ctx.Err() != nil {
			return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before approving")
		}
		if err := q.mutate(ctx, func(c context.Context) error { return q.c.Approve(c, pr.ID, pr.HeadOid) }); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "approved")
		refetch = true
	}
	if refetch {
		step("refreshing")
		var err error
		cur, err = q.refetch(ctx, pr)
		if ctx.Err() != nil {
			return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before merging")
		}
		if err != nil {
			return finish(r, model.ResultFailed, model.ReasonRefetchFailed, err.Error())
		}
		switch {
		case cur.State != "OPEN":
			return finish(r, model.ResultSkipped, model.ReasonNotOpen, "pull request is "+strings.ToLower(cur.State))
		case cur.HeadOid != pr.HeadOid:
			return finish(r, model.ResultFailed, model.ReasonHeadChanged, fmt.Sprintf("head moved from %s to %s", pr.HeadOid, cur.HeadOid))
		case cur.Checks.Failed > 0:
			return finish(r, model.ResultFailed, model.ReasonChecksFailing, fmt.Sprintf("%d failing check(s)", cur.Checks.Failed))
		case cur.AutoMerge:
			return finish(r, model.ResultSkipped, model.ReasonAlreadyMerging, "auto-merge was enabled meanwhile")
		case cur.MergeQueue:
			return finish(r, model.ResultSkipped, model.ReasonMergeQueue, "the base branch now uses a merge queue, which is not supported")
		}
	}
	method := MergeMethod(cur.RepoSettings)
	if method == "" {
		return finish(r, model.ResultSkipped, model.ReasonNoMergeMethod, "the repo allows no merge method")
	}
	label := strings.ToLower(method)
	kind, done, stop := finalMerge(r, cur)
	if stop {
		return done
	}
	if kind == mergeDirect {
		step("merging")
		if ctx.Err() != nil {
			return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before merging")
		}
		if err := q.mutate(ctx, func(c context.Context) error { return q.c.Merge(c, cur.ID, pr.HeadOid, method) }); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "merged ("+label+")")
	} else {
		step("enabling auto-merge")
		if ctx.Err() != nil {
			return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before merging")
		}
		if err := q.mutate(ctx, func(c context.Context) error { return q.c.EnableAutoMerge(c, cur.ID, pr.HeadOid, method) }); err != nil {
			return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error())
		}
		r.Steps = append(r.Steps, "auto-merge enabled ("+label+")")
	}
	return finish(r, model.ResultSuccess, "", "")
}

// fetch loads and enriches one PR.
func (q *Queue) fetch(ctx context.Context, ref model.PRRef) (*model.PR, error) {
	prs, warns, err := q.c.FetchPRs(ctx, q.o.Viewer, []model.PRRef{ref})
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
	triage.Enrich(prs[0])
	return prs[0], nil
}

// refetch reloads one PR, polling while GitHub still computes mergeability.
func (q *Queue) refetch(ctx context.Context, pr *model.PR) (*model.PR, error) {
	for attempt := 0; ; attempt++ {
		cur, err := q.fetch(ctx, pr.PRRef())
		if err != nil {
			return nil, err
		}
		if cur.MergeStateStatus != "UNKNOWN" || attempt >= unknownPolls {
			return cur, nil
		}
		if err := q.o.Sleep(ctx, q.o.PollInterval); err != nil {
			return nil, err
		}
	}
}

// approveCheck stops an approve that has nothing to do.
func approveCheck(r model.Result, pr *model.PR) (model.Result, bool) {
	if pr.ViewerApproved {
		return finish(r, model.ResultSkipped, model.ReasonAlreadyApproved, ""), true
	}
	return r, false
}

// mergeCheck stops a merge before any mutation when the outcome is already
// certain. BLOCKED is not stopped: the approval may unblock it.
func mergeCheck(r model.Result, pr *model.PR) (model.Result, bool) {
	switch {
	case pr.AutoMerge:
		return finish(r, model.ResultSkipped, model.ReasonAlreadyMerging, "auto-merge is already enabled"), true
	case pr.MergeQueue:
		return finish(r, model.ResultSkipped, model.ReasonMergeQueue, "the base branch uses a merge queue, which is not supported"), true
	case pr.MergeStateStatus == "DIRTY":
		return finish(r, model.ResultSkipped, model.ReasonNotMergeable, "merge conflicts with "+pr.BaseRef), true
	case MergeMethod(pr.RepoSettings) == "":
		return finish(r, model.ResultSkipped, model.ReasonNoMergeMethod, "the repo allows no merge method"), true
	}
	return r, false
}

type mergeKind int

const (
	mergeDirect mergeKind = iota + 1
	mergeAuto
)

// finalMerge picks how to merge cur in its current state.
func finalMerge(r model.Result, cur *model.PR) (mergeKind, model.Result, bool) {
	switch {
	case cur.MergeStateStatus == "CLEAN" || cur.MergeStateStatus == "HAS_HOOKS":
		return mergeDirect, r, false
	case cur.MergeStateStatus == "DIRTY":
		return 0, finish(r, model.ResultSkipped, model.ReasonNotMergeable, "merge conflicts with "+cur.BaseRef), true
	case cur.RepoSettings.AutoMergeAllowed:
		return mergeAuto, r, false
	default:
		return 0, finish(r, model.ResultSkipped, model.ReasonNotMergeable,
			fmt.Sprintf("merge state is %s and auto-merge is not allowed in this repo", cur.MergeStateStatus)), true
	}
}
