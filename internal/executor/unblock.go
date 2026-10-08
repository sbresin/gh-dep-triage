package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
)

// Renovate's rebase checkbox; Renovate rebases (or recreates) once it is ticked.
const (
	rebaseUnchecked = "- [ ] <!-- rebase-check -->"
	rebaseChecked   = "- [x] <!-- rebase-check -->"
)

// mutateStep reports name as the step and runs fn unless ctx is already
// cancelled. ok is false when r is finished (cancelled or failed).
func (q *Queue) mutateStep(ctx context.Context, r model.Result, name string, step func(string), fn func(context.Context) error) (model.Result, bool) {
	step(name)
	if ctx.Err() != nil {
		return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before "+name), false
	}
	if err := q.mutate(ctx, fn); err != nil {
		return finish(r, model.ResultFailed, model.ReasonMutationFailed, err.Error()), false
	}
	return r, true
}

type botAskKind int

const (
	askComment botAskKind = iota + 1
	askCheckbox
)

// botAsk decides how to ask pr's bot to rebase or recreate: the rebase
// checkbox for Renovate (any PR body that has one), a comment for Dependabot.
func botAsk(r model.Result, pr *model.PR, action string) (botAskKind, model.Result, bool) {
	// The checkbox decides, not the login: self-hosted Renovate apps have their own.
	hasBox := strings.Contains(strings.ToLower(pr.Body), "<!-- rebase-check -->")
	switch bot := policy.NormalizeLogin(pr.Author); {
	case bot == "dependabot" && !hasBox:
		return askComment, r, false
	case bot == "renovate" || hasBox:
		if strings.Contains(strings.ToLower(pr.Body), rebaseChecked) {
			return 0, finish(r, model.ResultSkipped, model.ReasonAlreadyRequested, "the rebase checkbox is already ticked"), true
		}
		if !strings.Contains(pr.Body, rebaseUnchecked) {
			return 0, finish(r, model.ResultFailed, model.ReasonNoRebaseCheckbox, "the PR description has no rebase checkbox"), true
		}
		return askCheckbox, r, false
	default:
		return 0, finish(r, model.ResultFailed, model.ReasonUnsupportedBot, fmt.Sprintf("can't ask %s to %s", pr.Author, action)), true
	}
}

func (q *Queue) botRequest(ctx context.Context, r model.Result, pr *model.PR, action string, step func(string)) model.Result {
	ask, done, stop := botAsk(r, pr, action)
	if stop {
		return done
	}
	fn := func(c context.Context) error { return q.c.Comment(c, pr.ID, "@dependabot "+action) }
	if ask == askCheckbox {
		body := strings.Replace(pr.Body, rebaseUnchecked, rebaseChecked, 1)
		fn = func(c context.Context) error { return q.c.UpdateBody(c, pr.ID, body) }
	}
	r, ok := q.mutateStep(ctx, r, "requesting "+action, step, fn)
	if !ok {
		return r
	}
	r.Steps = append(r.Steps, "requested "+action)
	return finish(r, model.ResultSuccess, "", "")
}

// rerun re-runs the failed jobs of every workflow run with a failed check.
// Failed checks outside GitHub Actions can't be re-run and are named in the message.
func (q *Queue) rerun(ctx context.Context, r model.Result, pr *model.PR, step func(string)) model.Result {
	runs, note, done, stop := rerunPlan(r, pr)
	if stop {
		return done
	}
	for _, id := range runs {
		var ok bool
		r, ok = q.mutateStep(ctx, r, fmt.Sprintf("re-running workflow run %d", id), step,
			func(c context.Context) error { return q.c.RerunFailedJobs(c, pr.Repo, id) })
		if !ok {
			return r
		}
		r.Steps = append(r.Steps, fmt.Sprintf("re-ran workflow run %d", id))
	}
	return finish(r, model.ResultSuccess, "", note)
}

func (q *Queue) requestReview(ctx context.Context, r model.Result, pr *model.PR, reviewer string, step func(string)) model.Result {
	step("looking up " + reviewer)
	id, team, err := q.c.ReviewerID(ctx, reviewer)
	switch {
	case ctx.Err() != nil:
		return finish(r, model.ResultSkipped, model.ReasonCancelled, "cancelled before requesting review")
	case errors.Is(err, github.ErrReviewerNotFound):
		return finish(r, model.ResultFailed, model.ReasonReviewerNotFound, err.Error())
	case err != nil:
		return finish(r, model.ResultFailed, model.ReasonMutationFailed, "look up "+reviewer+": "+err.Error())
	}
	users, teams := []string{id}, []string(nil)
	if team {
		users, teams = nil, []string{id}
	}
	r, ok := q.mutateStep(ctx, r, "requesting review", step, func(c context.Context) error {
		return q.c.RequestReviews(c, pr.ID, users, teams)
	})
	if !ok {
		return r
	}
	r.Steps = append(r.Steps, "requested review from "+reviewer)
	return finish(r, model.ResultSuccess, "", "")
}

// closePR comments with the matching blocker's detail, then closes. The
// policy check guarantees the blocker exists.
func (q *Queue) closePR(ctx context.Context, r model.Result, pr *model.PR, reason string, step func(string)) model.Result {
	detail := reason
	for _, b := range pr.Blockers {
		if b.Code == reason {
			detail = b.Detail
			break
		}
	}
	// detail embeds versions from bot titles; defuse @-mentions.
	body := "Closed by gh dep-triage: " + strings.ReplaceAll(detail, "@", "@\u200b") + "."
	r, ok := q.mutateStep(ctx, r, "commenting", step, func(c context.Context) error { return q.c.Comment(c, pr.ID, body) })
	if !ok {
		return r
	}
	r.Steps = append(r.Steps, "commented")
	if r, ok = q.mutateStep(ctx, r, "closing", step, func(c context.Context) error { return q.c.Close(c, pr.ID) }); !ok {
		return r
	}
	r.Steps = append(r.Steps, "closed")
	return finish(r, model.ResultSuccess, "", "")
}

// rerunPlan lists the workflow runs with a failed check, plus a note naming
// failed checks outside GitHub Actions.
func rerunPlan(r model.Result, pr *model.PR) ([]int64, string, model.Result, bool) {
	if pr.Checks.Failed == 0 {
		return nil, "", finish(r, model.ResultSkipped, model.ReasonNoFailedChecks, "no failing checks"), true
	}
	runs, other, seen := []int64{}, []string{}, map[int64]bool{}
	for _, c := range pr.CheckRuns {
		switch {
		case c.State != model.CheckStateFailed:
		case c.WorkflowRunID == 0:
			other = append(other, c.Name)
		case !seen[c.WorkflowRunID]:
			seen[c.WorkflowRunID] = true
			runs = append(runs, c.WorkflowRunID)
		}
	}
	note := ""
	if len(other) > 0 {
		note = "not GitHub Actions, re-run manually: " + strings.Join(other, ", ")
	}
	if len(runs) == 0 {
		return nil, note, finish(r, model.ResultSkipped, model.ReasonNotRerunnable, note), true
	}
	return runs, note, r, false
}
