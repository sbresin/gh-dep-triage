package triage

import (
	"fmt"
	"strings"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/parse"
)

const StaleAfter = 30 * 24 * time.Hour

var staleMarkers = []string{
	"will no longer update",
	"will not automatically rebase",
	"won't rebase",
	"no longer being updated",
}

var flakyConclusions = map[string]bool{"TIMED_OUT": true, "CANCELLED": true}

func Diagnose(groups []*model.Group, now time.Time) {
	newer := supersedingIndex(groups)
	for _, g := range groups {
		passed := passedByName(g)
		for _, pr := range g.PRs {
			pr.Blockers = diagnosePR(pr, passed, newer, now)
			pr.Status = statusOf(pr)
		}
	}
}

func CheckoutHint(pr *model.PR) string {
	return fmt.Sprintf("gh pr checkout %d -R %s", pr.Number, pr.Repo)
}

func command(action, ref string, extra ...string) string {
	parts := append([]string{"gh dep-triage", action, ref}, extra...)
	return strings.Join(append(parts, "--yes"), " ")
}

func diagnosePR(pr *model.PR, passed map[string]map[string]bool, newer map[string]*model.PR, now time.Time) []model.Blocker {
	blockers := []model.Blocker{}
	add := func(code, detail string, acts ...model.SuggestedAction) {
		if acts == nil {
			acts = []model.SuggestedAction{}
		}
		blockers = append(blockers, model.Blocker{Code: code, Detail: detail, SuggestedActions: acts})
	}

	if pr.Checks.Failed > 0 {
		add(model.BlockerChecksFailing,
			fmt.Sprintf("%d failing: %s", pr.Checks.Failed, strings.Join(pr.Checks.FailedNames, ", ")),
			failingActions(pr, passed)...)
	}
	if pr.MergeStateStatus == "BEHIND" {
		add(model.BlockerBehindBase, "Branch is behind "+pr.BaseRef,
			model.SuggestedAction{Action: "rebase", Command: command("rebase", pr.Ref)})
	}
	if pr.MergeStateStatus == "DIRTY" || pr.Mergeable == "CONFLICTING" {
		add(model.BlockerConflicts, "Merge conflicts with "+pr.BaseRef,
			model.SuggestedAction{Action: "rebase", Command: command("rebase", pr.Ref)},
			model.SuggestedAction{Action: "recreate", Command: command("recreate", pr.Ref)})
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" && pr.ViewerApproved {
		add(model.BlockerReviewRequired, "Approved by you; another approving review is required",
			model.SuggestedAction{Action: "request-review", Command: command("request-review", pr.Ref, "--reviewer <user|org/team>")})
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		add(model.BlockerChangesRequested, "Changes were requested")
	}
	if o := newer[pr.Ref]; o != nil {
		add(model.BlockerSuperseded, fmt.Sprintf("Superseded by %s (%s)", o.Ref, o.TargetVersion),
			model.SuggestedAction{Action: "close", Command: command("close", pr.Ref, "--reason superseded")})
	}
	if detail, ok := staleDetail(pr, now); ok {
		add(model.BlockerStale, detail,
			model.SuggestedAction{Action: "recreate", Command: command("recreate", pr.Ref)},
			model.SuggestedAction{Action: "close", Command: command("close", pr.Ref, "--reason stale")})
	}
	owesApproval := pr.ReviewDecision == "REVIEW_REQUIRED" && !pr.ViewerApproved
	if len(blockers) == 0 && pr.MergeStateStatus == "BLOCKED" && pr.Checks.Pending == 0 && !owesApproval {
		add(model.BlockerBlockedUnknown, "GitHub reports the PR as blocked for an unidentified reason")
	}
	return blockers
}

func failingActions(pr *model.PR, passed map[string]map[string]bool) []model.SuggestedAction {
	flaky, real := false, false
	for _, c := range pr.CheckRuns {
		if c.State != model.CheckStateFailed {
			continue
		}
		if flakyConclusions[strings.ToUpper(c.Conclusion)] || passesElsewhere(passed, c.Name, pr.Ref) {
			flaky = true
		} else {
			real = true
		}
	}
	acts := []model.SuggestedAction{}
	if flaky {
		acts = append(acts, model.SuggestedAction{Action: "rerun", Command: command("rerun", pr.Ref)})
	}
	if real {
		acts = append(acts, model.SuggestedAction{Action: "fix", Command: "gh dep-triage show " + pr.Ref + " --logs", Checkout: CheckoutHint(pr)})
	}
	return acts
}

func passedByName(g *model.Group) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, pr := range g.PRs {
		for _, c := range pr.CheckRuns {
			if c.State != model.CheckStatePassed {
				continue
			}
			if out[c.Name] == nil {
				out[c.Name] = map[string]bool{}
			}
			out[c.Name][pr.Ref] = true
		}
	}
	return out
}

func passesElsewhere(passed map[string]map[string]bool, name, ref string) bool {
	for r := range passed[name] {
		if r != ref {
			return true
		}
	}
	return false
}

func supersedingIndex(groups []*model.Group) map[string]*model.PR {
	byPkg := map[string][]*model.PR{}
	for _, g := range groups {
		for _, pr := range g.PRs {
			if pr.TargetVersion == "" {
				continue
			}
			k := strings.Join([]string{strings.ToLower(pr.Repo), pr.BaseRef, pr.Directory, pr.PackageKey}, "\x00")
			byPkg[k] = append(byPkg[k], pr)
		}
	}
	out := map[string]*model.PR{}
	for _, prs := range byPkg {
		for _, p := range prs {
			for _, o := range prs {
				if o != p && sameMajor(o, p) && isNewer(o, p) && (out[p.Ref] == nil || isNewer(o, out[p.Ref])) {
					out[p.Ref] = o
				}
			}
		}
	}
	return out
}

// sameMajor is false only when both target versions have differing major
// versions; incomparable versions fall back to isNewer's createdAt order.
func sameMajor(o, p *model.PR) bool {
	a, okA := parse.Major(o.TargetVersion)
	b, okB := parse.Major(p.TargetVersion)
	return !okA || !okB || a == b
}

func isNewer(o, p *model.PR) bool {
	if c, ok := parse.CompareVersions(o.TargetVersion, p.TargetVersion); ok && c != 0 {
		return c > 0
	}
	return o.CreatedAt.After(p.CreatedAt)
}

func staleDetail(pr *model.PR, now time.Time) (string, bool) {
	if age := now.Sub(pr.CreatedAt); age > StaleAfter {
		return fmt.Sprintf("Opened %d days ago", int(age.Hours()/24)), true
	}
	body := strings.ToLower(pr.Body)
	for _, m := range staleMarkers {
		if strings.Contains(body, m) {
			return "Bot reports it will no longer update this PR", true
		}
	}
	return "", false
}

func statusOf(pr *model.PR) model.Status {
	switch {
	case len(pr.Blockers) > 0:
		return model.StatusBlocked
	case pr.AutoMerge:
		return model.StatusMerging
	default:
		return model.StatusReady
	}
}
