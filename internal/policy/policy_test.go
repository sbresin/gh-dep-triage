package policy

import (
	"testing"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func pr(mods ...func(*model.PR)) *model.PR {
	p := &model.PR{Repo: "acme/api", Author: "dependabot", Bump: model.BumpPatch,
		Checks: model.CheckSummary{FailedNames: []string{}, PendingNames: []string{}}}
	for _, m := range mods {
		m(p)
	}
	return p
}

func TestEvaluate(t *testing.T) {
	soft := Rules{Soft: true}
	tests := []struct {
		name   string
		action string
		pr     *model.PR
		rules  Rules
		allow  bool
		reason string
	}{
		{"plain merge", model.ActionMerge, pr(), soft, true, ""},
		{"renovate ok", model.ActionApprove, pr(func(p *model.PR) { p.Author = "renovate" }), soft, true, ""},
		{"human PR", model.ActionApprove, pr(func(p *model.PR) { p.Author = "alice" }), Rules{}, false, model.ReasonNotBotPR},
		{"custom bot list", model.ActionMerge, pr(func(p *model.PR) { p.Author = "my-renovate" }), Rules{Bots: []string{"my-renovate[bot]"}}, true, ""},
		{"default bots replaced", model.ActionMerge, pr(), Rules{Bots: []string{"my-renovate"}}, false, model.ReasonNotBotPR},
		{"failing checks block merge", model.ActionMerge, pr(func(p *model.PR) {
			p.Checks.Failed, p.Checks.FailedNames = 1, []string{"test"}
		}), Rules{}, false, model.ReasonChecksFailing},
		{"failing checks allow approve", model.ActionApprove, pr(func(p *model.PR) { p.Checks.Failed = 1 }), soft, true, ""},
		{"failing checks block approve with automerge", model.ActionApprove, pr(func(p *model.PR) {
			p.AutoMerge, p.Checks.Failed, p.Checks.FailedNames = true, 1, []string{"test"}
		}), soft, false, model.ReasonChecksFailing},
		{"tui blocks approve with automerge and failing checks", model.ActionApprove, pr(func(p *model.PR) {
			p.AutoMerge, p.Checks.Failed, p.Checks.FailedNames = true, 1, []string{"test"}
		}), Rules{}, false, model.ReasonChecksFailing},
		{"failing checks allow approve in tui", model.ActionApprove, pr(func(p *model.PR) { p.Checks.Failed = 1 }), Rules{}, true, ""},
		{"major needs flag", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpMajor }), soft, false, model.ReasonMajorNeedsFlag},
		{"major with flag", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpMajor }), Rules{Soft: true, AllowMajor: true}, true, ""},
		{"major approve without automerge", model.ActionApprove, pr(func(p *model.PR) { p.Bump = model.BumpMajor }), soft, true, ""},
		{"major approve with automerge", model.ActionApprove, pr(func(p *model.PR) { p.Bump = model.BumpMajor; p.AutoMerge = true }), soft, false, model.ReasonMajorNeedsFlag},
		{"unknown bump without target needs flag", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpUnknown }), soft, false, model.ReasonMajorNeedsFlag},
		{"unknown bump without target approve with automerge", model.ActionApprove, pr(func(p *model.PR) { p.Bump = model.BumpUnknown; p.AutoMerge = true }), soft, false, model.ReasonMajorNeedsFlag},
		{"unknown bump without target approve", model.ActionApprove, pr(func(p *model.PR) { p.Bump = model.BumpUnknown }), soft, true, ""},
		{"unknown bump without target with flag", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpUnknown }), Rules{Soft: true, AllowMajor: true}, true, ""},
		{"unknown bump with target", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpUnknown; p.TargetVersion = "19.0.0" }), soft, true, ""},
		{"tui ignores unknown bump", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpUnknown }), Rules{}, true, ""},
		{"tui ignores major", model.ActionMerge, pr(func(p *model.PR) { p.Bump = model.BumpMajor }), Rules{}, true, ""},
		{"repo denied", model.ActionMerge, pr(), Rules{Soft: true, DenyRepos: []string{"acme/*"}}, false, model.ReasonRepoDenied},
		{"deny wins over allow", model.ActionMerge, pr(), Rules{Soft: true, AllowRepos: []string{"acme/*"}, DenyRepos: []string{"acme/api"}}, false, model.ReasonRepoDenied},
		{"not in allow list", model.ActionMerge, pr(), Rules{Soft: true, AllowRepos: []string{"other/*"}}, false, model.ReasonRepoNotAllowed},
		{"allow glob case-insensitive", model.ActionMerge, pr(func(p *model.PR) { p.Repo = "Acme/API" }), Rules{Soft: true, AllowRepos: []string{"acme/*"}}, true, ""},
		{"tui ignores repo lists", model.ActionMerge, pr(), Rules{DenyRepos: []string{"acme/*"}}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Evaluate(tt.action, tt.pr, tt.rules)
			if v.Allow != tt.allow || v.Reason != tt.reason {
				t.Errorf("got %+v, want allow=%v reason=%q", v, tt.allow, tt.reason)
			}
			if !v.Allow && v.Message == "" {
				t.Error("a denial needs a message")
			}
		})
	}
}
