package triage

import (
	"strings"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var statefulReviews = map[string]bool{"APPROVED": true, "CHANGES_REQUESTED": true, "DISMISSED": true}

// ViewerApproved reports whether the most recent stateful review is APPROVED.
func ViewerApproved(reviews []model.Review) bool {
	latest := ""
	var latestAt time.Time
	for _, r := range reviews {
		state := strings.ToUpper(r.State)
		if !statefulReviews[state] {
			continue
		}
		if !r.SubmittedAt.Before(latestAt) {
			latestAt, latest = r.SubmittedAt, state
		}
	}
	return latest == "APPROVED"
}
