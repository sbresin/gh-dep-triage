// Package plan parses plan documents and resolves them against a snapshot.
package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

type Item struct {
	Action  string         `json:"action"`
	Ref     string         `json:"ref"`
	Args    map[string]any `json:"args,omitempty"`
	HeadOid string         `json:"headOid,omitempty"`
}

// Task is one (action, PR) pair to evaluate and run, or — when Result is set —
// an outcome already decided during resolution.
type Task struct {
	Action string
	PR     *model.PR
	Result *model.Result
}

// Error is an invalid plan document or item.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func errorf(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// Items builds one item per ref for a command such as `merge <refs...>`.
func Items(action string, refs []string) []Item {
	items := make([]Item, len(refs))
	for i, r := range refs {
		items[i] = Item{Action: action, Ref: r}
	}
	return items
}

// Parse reads a JSON array of items, accepting only the given actions.
func Parse(r io.Reader, actions map[string]bool) ([]Item, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var items []Item
	if err := dec.Decode(&items); err != nil {
		return nil, errorf("plan must be a JSON array of {action, ref, args?, headOid?}: %v", err)
	}
	if len(items) == 0 {
		return nil, errorf("plan has no items")
	}
	for i, it := range items {
		if !actions[it.Action] {
			return nil, errorf("item %d: unsupported action %q", i, it.Action)
		}
		if strings.TrimSpace(it.Ref) == "" {
			return nil, errorf("item %d: ref is required", i)
		}
	}
	return items, nil
}

func preResult(action, ref, status, reason, message string) Task {
	return Task{Action: action, Result: &model.Result{Action: action, Ref: ref, Status: status, Reason: reason,
		Message: message, Steps: []string{}}}
}

// Resolve expands refs against snap, deduplicates (action, PR) pairs and
// records resolution-time failures as Task results.
func Resolve(snap *model.Snapshot, items []Item) ([]Task, error) {
	tasks := []Task{}
	seen := map[string]bool{}
	for _, it := range items {
		ref, err := triage.ParseRef(strings.TrimSpace(it.Ref))
		if err != nil {
			return nil, err
		}
		var prs []*model.PR
		switch ref.Kind {
		case triage.RefGroup:
			if it.HeadOid != "" {
				return nil, errorf("headOid needs a PR ref, not group %s", ref.Raw)
			}
			g, err := triage.ResolveGroup(snap.Groups, ref)
			if err != nil {
				return nil, err
			}
			prs = g.PRs
		default:
			pr := snap.Find(ref.PR)
			if pr == nil {
				tasks = append(tasks, preResult(it.Action, ref.PR.String(), model.ResultFailed, model.ReasonNotEligible,
					"not in your triage snapshot (no review requested and not approved by you)"))
				continue
			}
			prs = []*model.PR{pr}
		}
		for _, pr := range prs {
			key := it.Action + " " + strings.ToLower(pr.Ref)
			if seen[key] {
				continue
			}
			seen[key] = true
			if it.HeadOid != "" && it.HeadOid != pr.HeadOid {
				tasks = append(tasks, preResult(it.Action, pr.Ref, model.ResultFailed, model.ReasonHeadChanged,
					fmt.Sprintf("head moved from %s to %s since the plan was made", it.HeadOid, pr.HeadOid)))
				continue
			}
			tasks = append(tasks, Task{Action: it.Action, PR: pr})
		}
	}
	return tasks, nil
}
