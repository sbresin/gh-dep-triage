package tui

import (
	"fmt"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// mkPR returns an enriched, ready, viewer-not-yet-approved Dependabot PR.
func mkPR(repo string, n int, title string, mods ...func(*model.PR)) *model.PR {
	p := &model.PR{
		ID: fmt.Sprintf("PR_%s#%d", repo, n), Repo: repo, Number: n, Ref: fmt.Sprintf("%s#%d", repo, n),
		Title: title, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, n), Author: "dependabot",
		State: "OPEN", CreatedAt: testNow.Add(-24 * time.Hour), HeadOid: fmt.Sprintf("sha%d", n), BaseRef: "main",
		MergeStateStatus: "BLOCKED", Mergeable: "MERGEABLE", ReviewDecision: "REVIEW_REQUIRED",
		RequestedForReview: true,
		RepoSettings:       model.RepoSettings{SquashMergeAllowed: true, AutoMergeAllowed: true, ViewerDefaultMergeMethod: "SQUASH"},
	}
	for _, m := range mods {
		m(p)
	}
	triage.Enrich(p)
	return p
}

func snapshot(prs ...*model.PR) *model.Snapshot {
	return &model.Snapshot{Viewer: "octocat", GeneratedAt: testNow, Groups: triage.Build(prs, testNow), Warnings: []model.Problem{}}
}

// Fixture used across tui tests:
//
//	acme/api#1, acme/web#2  lodash 4.17.20 -> 4.17.21 (group of two, ready)
//	acme/api#3              axios 1.6.0 -> 1.7.0, behind base (blocked)
//	acme/api#4              eslint 8.57.0 -> 9.1.0, auto-merge on (merging)
func fixture() *model.Snapshot {
	return snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0", func(p *model.PR) { p.MergeStateStatus = "BEHIND" }),
		mkPR("acme/api", 4, "Bump eslint from 8.57.0 to 9.1.0", func(p *model.PR) {
			p.AutoMerge = true
			p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
			p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow}}
		}),
	)
}
