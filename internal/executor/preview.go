package executor

import (
	"fmt"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Preview predicts the result of running action on pr from the snapshot
// alone: the steps the job would take, or the stop that its pre-mutation
// checks already show. Planned steps are imperative ("approve") so they can't
// be mistaken for executed ones ("approved").
func Preview(action string, pr *model.PR, args map[string]string) model.Result {
	r := newResult(action, pr, args)
	planned := func(steps ...string) model.Result {
		r.Status, r.Steps = model.ResultPlanned, append([]string{}, steps...)
		return r
	}
	switch action {
	case model.ActionApprove:
		if done, stop := approveCheck(r, pr); stop {
			return done
		}
		return planned("approve")
	case model.ActionMerge:
		return previewMerge(r, pr)
	case model.ActionRebase, model.ActionRecreate:
		ask, done, stop := botAsk(r, pr, action)
		if stop {
			return done
		}
		if ask == askComment {
			return planned("comment @dependabot " + action)
		}
		return planned("tick the rebase checkbox")
	case model.ActionRerun:
		runs, note, done, stop := rerunPlan(r, pr)
		if stop {
			return done
		}
		steps := make([]string, len(runs))
		for i, id := range runs {
			steps[i] = fmt.Sprintf("re-run workflow run %d", id)
		}
		r.Message = note
		return planned(steps...)
	case model.ActionRequestReview:
		return planned("request review from " + args["reviewer"])
	case model.ActionClose:
		return planned("comment", "close")
	}
	return planned()
}

func previewMerge(r model.Result, pr *model.PR) model.Result {
	if done, stop := mergeCheck(r, pr); stop {
		return done
	}
	m := strings.ToLower(MergeMethod(pr.RepoSettings))
	r.Status = model.ResultPlanned
	// Approval (or GitHub still computing the state) decides the final step.
	if !pr.ViewerApproved || pr.MergeStateStatus == "UNKNOWN" {
		then := "merge (" + m + ") if clean, else enable auto-merge (" + m + ")"
		if !pr.RepoSettings.AutoMergeAllowed {
			then = "merge (" + m + ") if clean, else skip"
		}
		if !pr.ViewerApproved {
			r.Steps = append(r.Steps, "approve")
		}
		r.Steps = append(r.Steps, then)
		return r
	}
	kind, done, stop := finalMerge(r, pr)
	if stop {
		return done
	}
	if kind == mergeDirect {
		r.Steps = append(r.Steps, "merge ("+m+")")
	} else {
		r.Steps = append(r.Steps, "enable auto-merge ("+m+")")
	}
	return r
}
