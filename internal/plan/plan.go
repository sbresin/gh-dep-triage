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
	Action  string            `json:"action"`
	Ref     string            `json:"ref"`
	Args    map[string]string `json:"args,omitempty"`
	HeadOid string            `json:"headOid,omitempty"`
}

// Task is one (action, PR) pair to evaluate and run, or — when Result is set —
// an outcome already decided during resolution.
type Task struct {
	Action string
	Args   map[string]string
	PR     *model.PR
	Result *model.Result
}

// Error is an invalid plan document or item.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func errorf(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// Items builds one item per ref for a command such as `merge <refs...>`.
func Items(action string, refs []string, args map[string]string) []Item {
	items := make([]Item, len(refs))
	for i, r := range refs {
		items[i] = Item{Action: action, Ref: r, Args: args}
	}
	return items
}

// argKeys is the one required arg of each action that takes one.
var argKeys = map[string]string{model.ActionRequestReview: "reviewer", model.ActionClose: "reason"}

// CheckArgs validates args for action: the action's one required arg, nothing else.
func CheckArgs(action string, args map[string]string) error {
	want := argKeys[action]
	for k := range args {
		if k != want {
			return errorf("%s takes no %q arg", action, k)
		}
	}
	if want == "" {
		return nil
	}
	v := args[want]
	switch {
	case v == "":
		return errorf("%s needs %q (--%s)", action, want, want)
	case want == "reason" && v != model.BlockerSuperseded && v != model.BlockerStale:
		return errorf("reason %q must be superseded or stale", v)
	case want == "reviewer" && strings.EqualFold(v, "REVIEWER"):
		return errorf("replace the REVIEWER placeholder with a user login or org/team")
	case want == "reviewer" && !ValidTeam(v) && strings.ContainsAny(v, "/ \t\r\n"):
		return errorf("reviewer %q must be a user login or org/team", v)
	}
	return nil
}

// ValidTeam reports whether s is org/team: one slash, both sides non-empty, no whitespace.
func ValidTeam(s string) bool {
	org, team, ok := strings.Cut(s, "/")
	return ok && org != "" && team != "" && !strings.Contains(team, "/") && !strings.ContainsAny(s, " \t\r\n")
}

// Parse reads a JSON array of items, accepting only the given actions.
func Parse(r io.Reader, actions map[string]bool) ([]Item, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var items []Item
	if err := dec.Decode(&items); err != nil {
		return nil, errorf("plan must be a JSON array of {action, ref, args?, headOid?}: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errorf("plan must be a single JSON array: unexpected content after it")
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
		if err := CheckArgs(it.Action, it.Args); err != nil {
			return nil, errorf("item %d: %v", i, err)
		}
	}
	return items, nil
}

func preResult(action string, args map[string]string, ref, status, reason, message string) Task {
	return Task{Action: action, Args: args, Result: &model.Result{Action: action, Ref: ref, Args: args, Status: status, Reason: reason,
		Message: message, Steps: []string{}}}
}

// Resolve expands refs against snap, deduplicates (action, PR) pairs and
// records resolution-time failures as Task results. A duplicate keeps the
// position of its first occurrence, but a stale headOid on any occurrence
// turns the pair into a head_changed failure.
func Resolve(snap *model.Snapshot, items []Item) ([]Task, error) {
	tasks := []Task{}
	seen := map[string]int{}
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
				key := it.Action + " " + strings.ToLower(ref.PR.String()) + " " + fmt.Sprint(it.Args)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = len(tasks)
				tasks = append(tasks, preResult(it.Action, it.Args, ref.PR.String(), model.ResultFailed, model.ReasonNotEligible,
					"not in your triage snapshot (no review requested and not approved by you)"))
				continue
			}
			prs = []*model.PR{pr}
		}
		for _, pr := range prs {
			key := it.Action + " " + strings.ToLower(pr.Ref) + " " + fmt.Sprint(it.Args)
			task := Task{Action: it.Action, Args: it.Args, PR: pr}
			stale := it.HeadOid != "" && it.HeadOid != pr.HeadOid
			if stale {
				task = preResult(it.Action, it.Args, pr.Ref, model.ResultFailed, model.ReasonHeadChanged,
					fmt.Sprintf("head moved from %s to %s since the plan was made", it.HeadOid, pr.HeadOid))
			}
			if i, dup := seen[key]; dup {
				if stale && tasks[i].Result == nil {
					tasks[i] = task
				}
				continue
			}
			seen[key] = len(tasks)
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}
