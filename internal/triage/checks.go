// Package triage holds pure logic: enrichment, grouping, blockers and refs.
package triage

import (
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var (
	failStates    = map[string]bool{"FAILURE": true, "ERROR": true, "TIMED_OUT": true, "ACTION_REQUIRED": true, "CANCELLED": true, "STARTUP_FAILURE": true}
	pendingStates = map[string]bool{"IN_PROGRESS": true, "QUEUED": true, "PENDING": true, "REQUESTED": true, "WAITING": true, "EXPECTED": true}
)

func ClassifyCheck(c model.Check) string {
	status, conclusion := strings.ToUpper(c.Status), strings.ToUpper(c.Conclusion)
	if c.Kind == model.CheckKindRun && status != "" && status != "COMPLETED" {
		return model.CheckStatePending
	}
	switch {
	case failStates[conclusion]:
		return model.CheckStateFailed
	case c.Kind == model.CheckKindStatus && pendingStates[conclusion]:
		return model.CheckStatePending
	case conclusion == "SUCCESS":
		return model.CheckStatePassed
	default:
		return model.CheckStateSkipped
	}
}

// SummarizeChecks sets State on every check in place and returns the counts.
func SummarizeChecks(checks []model.Check) model.CheckSummary {
	s := model.CheckSummary{FailedNames: []string{}, PendingNames: []string{}}
	for i := range checks {
		checks[i].State = ClassifyCheck(checks[i])
		s.Total++
		switch checks[i].State {
		case model.CheckStateFailed:
			s.Failed++
			s.FailedNames = append(s.FailedNames, checks[i].Name)
		case model.CheckStatePending:
			s.Pending++
			s.PendingNames = append(s.PendingNames, checks[i].Name)
		case model.CheckStatePassed:
			s.Passed++
		default:
			s.Skipped++
		}
	}
	return s
}
