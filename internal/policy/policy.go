// Package policy decides whether a planned action may run.
package policy

import (
	"fmt"
	"path"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var defaultBots = []string{"dependabot", "renovate"}

// Rules configures Evaluate. Soft enables the CLI-only rules (repo lists and
// --allow-major); the TUI evaluates hard rules only.
type Rules struct {
	Bots       []string
	Soft       bool
	AllowMajor bool
	AllowRepos []string
	DenyRepos  []string
}

type Verdict struct {
	Allow   bool
	Reason  string
	Message string
}

func deny(reason, format string, args ...any) Verdict {
	return Verdict{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

func Evaluate(action string, pr *model.PR, args map[string]string, r Rules) Verdict {
	if !isBot(pr.Author, r.Bots) {
		return deny(model.ReasonNotBotPR, "only bot PRs are supported (author %q)", pr.Author)
	}
	// Approving a PR with auto-merge on would merge it despite the failure.
	if pr.Checks.Failed > 0 && (action == model.ActionMerge || (action == model.ActionApprove && pr.AutoMerge)) {
		return deny(model.ReasonChecksFailing, "%d failing check(s): %s", pr.Checks.Failed, strings.Join(pr.Checks.FailedNames, ", "))
	}
	if action == model.ActionClose && !pr.HasBlocker(args["reason"]) {
		return deny(model.ReasonBlockerMissing, "%s has no %s blocker", pr.Ref, args["reason"])
	}
	if !r.Soft {
		return Verdict{Allow: true}
	}
	if matchAny(r.DenyRepos, pr.Repo) {
		return deny(model.ReasonRepoDenied, "%s is on the repo deny list", pr.Repo)
	}
	if len(r.AllowRepos) > 0 && !matchAny(r.AllowRepos, pr.Repo) {
		return deny(model.ReasonRepoNotAllowed, "%s is not on the repo allow list", pr.Repo)
	}
	if !r.AllowMajor && (action == model.ActionMerge || (action == model.ActionApprove && pr.AutoMerge)) {
		if pr.Bump == model.BumpMajor {
			return deny(model.ReasonMajorNeedsFlag, "major bump; pass --allow-major to %s it", action)
		}
		if pr.Bump == model.BumpUnknown && pr.TargetVersion == "" {
			return deny(model.ReasonMajorNeedsFlag, "bump type unknown; pass --allow-major to %s it", action)
		}
	}
	return Verdict{Allow: true}
}

// NormalizeLogin lower-cases a login and strips a "[bot]" suffix.
func NormalizeLogin(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "[bot]")
}

func isBot(author string, bots []string) bool {
	if len(bots) == 0 {
		bots = defaultBots
	}
	a := NormalizeLogin(author)
	for _, b := range bots {
		if NormalizeLogin(b) == a {
			return true
		}
	}
	return false
}

func matchAny(patterns []string, repo string) bool {
	repo = strings.ToLower(repo)
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), repo); ok {
			return true
		}
	}
	return false
}
