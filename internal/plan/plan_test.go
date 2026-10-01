package plan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

var actions = map[string]bool{model.ActionApprove: true, model.ActionMerge: true}

func snapshot() *model.Snapshot {
	mk := func(repo string, n int, title, head string) *model.PR {
		p := &model.PR{Repo: repo, Number: n, Ref: (model.PRRef{Repo: repo, Number: n}).String(), Title: title,
			Author: "dependabot", State: "OPEN", HeadOid: head, CreatedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
		triage.Enrich(p)
		return p
	}
	prs := []*model.PR{
		mk("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", "h1"),
		mk("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21", "h2"),
		mk("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0", "h3"),
	}
	return &model.Snapshot{Groups: triage.Build(prs, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))}
}

type view struct{ Action, Ref, Status, Reason string }

func views(ts []Task) []view {
	out := []view{}
	for _, t := range ts {
		if t.Result != nil {
			out = append(out, view{t.Action, t.Result.Ref, t.Result.Status, t.Result.Reason})
		} else {
			out = append(out, view{t.Action, t.PR.Ref, "", ""})
		}
	}
	return out
}

func TestParse(t *testing.T) {
	items, err := Parse(strings.NewReader(`[{"action":"merge","ref":" acme/api#1 ","headOid":"h1"},{"action":"approve","ref":"group:axios@1.7.0"}]`), actions)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{{Action: "merge", Ref: " acme/api#1 ", HeadOid: "h1"}, {Action: "approve", Ref: "group:axios@1.7.0"}}
	if diff := cmp.Diff(want, items); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestParseRejects(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `nope`,
		"object":         `{"action":"merge","ref":"acme/api#1"}`,
		"empty":          `[]`,
		"unknown action": `[{"action":"rebase","ref":"acme/api#1"}]`,
		"missing ref":    `[{"action":"merge"}]`,
		"unknown field":  `[{"action":"merge","ref":"acme/api#1","sha":"x"}]`,
	} {
		_, err := Parse(strings.NewReader(body), actions)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%s: want *plan.Error, got %v", name, err)
		}
	}
}

func TestResolveExpandsGroupsAndOrders(t *testing.T) {
	tasks, err := Resolve(snapshot(), Items("merge", []string{"group:lodash@4.17.21", "acme/api#3"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []view{{"merge", "acme/api#1", "", ""}, {"merge", "acme/web#2", "", ""}, {"merge", "acme/api#3", "", ""}}
	if diff := cmp.Diff(want, views(tasks)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestResolveDedupes(t *testing.T) {
	tasks, err := Resolve(snapshot(), Items("merge", []string{"acme/api#1", "group:lodash@4.17.21", "acme/api#1"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks: %+v", len(tasks), views(tasks))
	}
}

func TestResolvePreFailures(t *testing.T) {
	items := []Item{
		{Action: "merge", Ref: "acme/api#99"},
		{Action: "merge", Ref: "acme/api#1", HeadOid: "old"},
		{Action: "merge", Ref: "acme/web#2", HeadOid: "h2"},
	}
	tasks, err := Resolve(snapshot(), items)
	if err != nil {
		t.Fatal(err)
	}
	want := []view{
		{"merge", "acme/api#99", model.ResultFailed, model.ReasonNotEligible},
		{"merge", "acme/api#1", model.ResultFailed, model.ReasonHeadChanged},
		{"merge", "acme/web#2", "", ""},
	}
	if diff := cmp.Diff(want, views(tasks)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if tasks[1].Result.Steps == nil || !strings.Contains(tasks[1].Result.Message, "old") {
		t.Errorf("head_changed result: %+v", tasks[1].Result)
	}
}

func TestResolveErrors(t *testing.T) {
	var re *triage.RefError
	if _, err := Resolve(snapshot(), Items("merge", []string{"nonsense"})); !errors.As(err, &re) || re.Code != "invalid_ref" {
		t.Errorf("invalid ref: %v", err)
	}
	if _, err := Resolve(snapshot(), Items("merge", []string{"group:react@1.0.0"})); !errors.As(err, &re) || re.Code != "not_found" {
		t.Errorf("missing group: %v", err)
	}
	var pe *Error
	if _, err := Resolve(snapshot(), []Item{{Action: "merge", Ref: "group:axios@1.7.0", HeadOid: "h3"}}); !errors.As(err, &pe) {
		t.Errorf("headOid on group: %v", err)
	}
}
