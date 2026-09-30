package triage

import (
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Build infers bump types, groups and diagnoses already-enriched PRs.
func Build(prs []*model.PR, now time.Time) []*model.Group {
	InferBumps(prs)
	groups := GroupPRs(prs)
	Diagnose(groups, now)
	return groups
}
