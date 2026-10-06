package triage

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

const notes = "<details>\n<summary>Release notes</summary>\n<p><em>Sourced from <a href=\"x\">idna's releases</a>.</em></p>\n<blockquote>\n<h2>v3.11</h2>\n<p>Fix a bug.</p>\n</blockquote>\n</details>"

func autoPR(mods ...func(*model.PR)) *model.PR {
	return testPR("acme/api", 1, "Bump idna from 3.10 to 3.11", append([]func(*model.PR){func(p *model.PR) {
		p.Body = notes
		p.Files, p.FileCount = []string{"uv.lock", "pyproject.toml"}, 2
		p.CheckRuns = []model.Check{checkRun("test", "COMPLETED", "SUCCESS")}
		p.Risk = &model.Risk{System: "PYPI", SourceRepo: "github.com/kjd/idna", Stars: 300, PublishedAt: testNow}
	}}, mods...)...)
}

func TestTier(t *testing.T) {
	required := func(c model.Check) model.Check { c.Required = true; return c }
	tests := []struct {
		name    string
		pr      *model.PR
		tier    string
		reasons []string
	}{
		{"auto", autoPR(), TierAuto, nil},
		{"workflow file", autoPR(func(p *model.PR) { p.Files, p.FileCount = []string{".github/workflows/ci.yml"}, 1 }), TierAuto, nil},
		{"nested lockfile", autoPR(func(p *model.PR) { p.Files = []string{"web/package-lock.json", "web/package.json"} }), TierAuto, nil},
		{"denied", autoPR(func(p *model.PR) { p.MergeDenied = model.ReasonMajorNeedsFlag }), TierManual, []string{model.ReasonMajorNeedsFlag}},
		{"major", autoPR(func(p *model.PR) { p.Title = "Bump idna from 3.10 to 4.0" }), TierReview, []string{"bump_major"}},
		{"no risk", autoPR(func(p *model.PR) { p.Risk = nil }), TierReview, []string{"no_depsdev_data"}},
		{"unknown version", autoPR(func(p *model.PR) { p.Risk.PublishedAt = time.Time{} }), TierReview, []string{"depsdev_unknown_version"}},
		{"no stars", autoPR(func(p *model.PR) { p.Risk.Stars = 0 }), TierReview, []string{"no_source_repo"}},
		{"findings", autoPR(func(p *model.PR) { p.Risk.Findings = []string{"COOLDOWN", "LOW_USAGE"} }), TierReview, []string{"finding_COOLDOWN", "finding_LOW_USAGE"}},
		{"advisory", autoPR(func(p *model.PR) { p.Risk.Advisories = []string{"GHSA-1"} }), TierReview, []string{"advisory"}},
		{"deprecated", autoPR(func(p *model.PR) { p.Risk.Deprecated = true }), TierReview, []string{"deprecated"}},
		{"pending", autoPR(func(p *model.PR) {
			p.CheckRuns = append(p.CheckRuns, checkRun("lint", "IN_PROGRESS", ""))
		}), TierReview, []string{"checks_pending"}},
		{"no checks", autoPR(func(p *model.PR) { p.CheckRuns = nil }), TierReview, []string{"untested"}},
		{"required skipped", autoPR(func(p *model.PR) {
			p.CheckRuns = []model.Check{checkRun("lint", "COMPLETED", "SUCCESS"), required(checkRun("everything", "COMPLETED", "SKIPPED"))}
		}), TierReview, []string{"untested"}},
		{"required passed", autoPR(func(p *model.PR) {
			p.CheckRuns = []model.Check{required(checkRun("everything", "COMPLETED", "SUCCESS")), checkRun("x", "COMPLETED", "SKIPPED")}
		}), TierAuto, nil},
		{"source file", autoPR(func(p *model.PR) { p.Files = append(p.Files, "src/main.py") }), TierReview, []string{"non_manifest_files"}},
		{"too many files", autoPR(func(p *model.PR) { p.FileCount = 101 }), TierReview, []string{"non_manifest_files"}},
		{"no files", autoPR(func(p *model.PR) { p.Files, p.FileCount = nil, 0 }), TierReview, []string{"non_manifest_files"}},
		{"no notes", autoPR(func(p *model.PR) { p.Body = "Bumps idna." }), TierReview, []string{"no_release_notes"}},
		{"keyword", autoPR(func(p *model.PR) {
			p.Body = notes[:len(notes)-30] + "BREAKING: drop py3.8</p>\n</blockquote>\n</details>"
		}), TierReview, []string{"changelog_keyword"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier, reasons := Tier(tt.pr)
			if tier != tt.tier || !cmp.Equal(reasons, tt.reasons) {
				t.Errorf("Tier = %s %v, want %s %v", tier, reasons, tt.tier, tt.reasons)
			}
		})
	}
}
