package triage

import (
	"fmt"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var testNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// testPR builds an enriched PR that is ready by default: clean, approved by
// GitHub's rules, one day old.
func testPR(repo string, n int, title string, mods ...func(*model.PR)) *model.PR {
	p := &model.PR{
		Repo: repo, Number: n, Ref: fmt.Sprintf("%s#%d", repo, n), Title: title,
		Author: "dependabot", State: "OPEN", BaseRef: "main",
		CreatedAt:        testNow.Add(-24 * time.Hour),
		MergeStateStatus: "CLEAN", Mergeable: "MERGEABLE", ReviewDecision: "APPROVED",
	}
	for _, m := range mods {
		m(p)
	}
	Enrich(p)
	return p
}

func checkRun(name, status, conclusion string) model.Check {
	return model.Check{Name: name, Kind: model.CheckKindRun, Status: status, Conclusion: conclusion}
}

func statusCtx(name, state string) model.Check {
	return model.Check{Name: name, Kind: model.CheckKindStatus, Conclusion: state}
}
