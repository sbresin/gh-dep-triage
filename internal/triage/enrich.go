package triage

import (
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/parse"
)

// Enrich derives parsed and summarised fields from the raw GitHub data.
func Enrich(pr *model.PR) {
	t := parse.ParseTitle(pr.Title)
	pr.Package, pr.PackageKey = t.Package, t.PackageKey
	pr.SourceVersion, pr.TargetVersion, pr.Bump = t.Source, t.Target, t.Bump
	pr.Directory = t.Directory
	if b, ok := parse.RenovateBadge(pr.Body); ok && pr.SourceVersion == "" && b.From != "" && parse.BadgeMatches(b, pr.TargetVersion) {
		pr.SourceVersion, pr.Bump = b.From, parse.ClassifyBump(b.From, b.To)
	}
	if pr.CheckRuns == nil {
		pr.CheckRuns = []model.Check{}
	}
	if pr.RequestedReviewers == nil {
		pr.RequestedReviewers = []string{}
	}
	pr.Checks = SummarizeChecks(pr.CheckRuns)
	pr.ViewerApproved = ViewerApproved(pr.ViewerReviews)
	pr.Blockers = []model.Blocker{}
}
