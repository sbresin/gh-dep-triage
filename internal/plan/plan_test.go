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
		"trailing data":  `[{"action":"merge","ref":"acme/api#1"}] garbage`,
		"second array":   `[{"action":"merge","ref":"acme/api#1"}][]`,
	} {
		_, err := Parse(strings.NewReader(body), actions)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%s: want *plan.Error, got %v", name, err)
		}
	}
}

func TestResolveExpandsGroupsAndOrders(t *testing.T) {
	tasks, err := Resolve(snapshot(), Items("merge", []string{"group:lodash@4.17.21", "acme/api#3"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []view{{"merge", "acme/api#1", "", ""}, {"merge", "acme/web#2", "", ""}, {"merge", "acme/api#3", "", ""}}
	if diff := cmp.Diff(want, views(tasks)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestResolveDedupes(t *testing.T) {
	tasks, err := Resolve(snapshot(), Items("merge", []string{"acme/api#1", "group:lodash@4.17.21", "acme/api#1"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []view{{"merge", "acme/api#1", "", ""}, {"merge", "acme/web#2", "", ""}}
	if diff := cmp.Diff(want, views(tasks)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestResolveDedupeHonoursPinnedHead(t *testing.T) {
	stale := []view{{"merge", "acme/api#1", model.ResultFailed, model.ReasonHeadChanged}, {"merge", "acme/web#2", "", ""}}
	for name, tc := range map[string]struct {
		items []Item
		want  []view
	}{
		"group then pinned stale": {
			[]Item{{Action: "merge", Ref: "group:lodash@4.17.21"}, {Action: "merge", Ref: "acme/api#1", HeadOid: "old"}},
			stale,
		},
		"pinned stale then group": {
			[]Item{{Action: "merge", Ref: "acme/api#1", HeadOid: "old"}, {Action: "merge", Ref: "group:lodash@4.17.21"}},
			stale,
		},
		"pinned matching and group": {
			[]Item{{Action: "merge", Ref: "group:lodash@4.17.21"}, {Action: "merge", Ref: "acme/api#1", HeadOid: "h1"}},
			[]view{{"merge", "acme/api#1", "", ""}, {"merge", "acme/web#2", "", ""}},
		},
		"case-insensitive duplicate": {
			[]Item{{Action: "merge", Ref: "ACME/api#1"}, {Action: "merge", Ref: "acme/api#1", HeadOid: "old"}},
			[]view{{"merge", "acme/api#1", model.ResultFailed, model.ReasonHeadChanged}},
		},
		"duplicate missing ref": {
			Items("merge", []string{"acme/api#99", "ACME/api#99"}, nil),
			[]view{{"merge", "acme/api#99", model.ResultFailed, model.ReasonNotEligible}},
		},
	} {
		tasks, err := Resolve(snapshot(), tc.items)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if diff := cmp.Diff(tc.want, views(tasks)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", name, diff)
		}
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
	if _, err := Resolve(snapshot(), Items("merge", []string{"nonsense"}, nil)); !errors.As(err, &re) || re.Code != "invalid_ref" {
		t.Errorf("invalid ref: %v", err)
	}
	if _, err := Resolve(snapshot(), Items("merge", []string{"group:react@1.0.0"}, nil)); !errors.As(err, &re) || re.Code != "not_found" {
		t.Errorf("missing group: %v", err)
	}
	var pe *Error
	if _, err := Resolve(snapshot(), []Item{{Action: "merge", Ref: "group:axios@1.7.0", HeadOid: "h3"}}); !errors.As(err, &pe) {
		t.Errorf("headOid on group: %v", err)
	}
}

func TestCheckArgs(t *testing.T) {
	ok := []struct {
		action string
		args   map[string]string
	}{
		{"merge", nil},
		{"rebase", map[string]string{}},
		{"request-review", map[string]string{"reviewer": "alice"}},
		{"request-review", map[string]string{"reviewer": "acme/platform"}},
		{"close", map[string]string{"reason": "superseded"}},
		{"close", map[string]string{"reason": "stale"}},
	}
	for _, tc := range ok {
		if err := CheckArgs(tc.action, tc.args); err != nil {
			t.Errorf("%s %v: %v", tc.action, tc.args, err)
		}
	}
	bad := []struct {
		action string
		args   map[string]string
		msg    string
	}{
		{"merge", map[string]string{"reason": "stale"}, `merge takes no "reason" arg`},
		{"request-review", nil, `request-review needs "reviewer" (--reviewer)`},
		{"request-review", map[string]string{"reviewer": ""}, `needs "reviewer"`},
		{"request-review", map[string]string{"reviewer": "al ice"}, "must be a user login or org/team"},
		{"request-review", map[string]string{"reviewer": "/team"}, "must be a user login or org/team"},
		{"request-review", map[string]string{"reviewer": "org/"}, "must be a user login or org/team"},
		{"request-review", map[string]string{"reviewer": "a/b/c"}, "must be a user login or org/team"},
		{"request-review", map[string]string{"reviewer": "alice", "reason": "x"}, `takes no "reason" arg`},
		{"close", map[string]string{"reason": "bogus"}, `reason "bogus" must be superseded or stale`},
		{"close", nil, `close needs "reason" (--reason)`},
	}
	for _, tc := range bad {
		err := CheckArgs(tc.action, tc.args)
		var pe *Error
		if !errors.As(err, &pe) || !strings.Contains(pe.Message, tc.msg) {
			t.Errorf("%s %v: got %v, want *Error containing %q", tc.action, tc.args, err, tc.msg)
		}
	}
}

func TestValidTeam(t *testing.T) {
	for s, want := range map[string]bool{"acme/platform": true, "acme": false, "/x": false, "x/": false, "a/b/c": false, "a /b": false, "": false} {
		if got := ValidTeam(s); got != want {
			t.Errorf("ValidTeam(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseArgs(t *testing.T) {
	all := map[string]bool{"close": true, "request-review": true}
	items, err := Parse(strings.NewReader(`[{"action":"close","ref":"acme/api#1","args":{"reason":"superseded"}}]`), all)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]Item{{Action: "close", Ref: "acme/api#1", Args: map[string]string{"reason": "superseded"}}}, items); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	for name, body := range map[string]string{
		"missing reviewer": `[{"action":"request-review","ref":"acme/api#1"}]`,
		"non-string arg":   `[{"action":"close","ref":"acme/api#1","args":{"reason":1}}]`,
		"bad reason":       `[{"action":"close","ref":"acme/api#1","args":{"reason":"nope"}}]`,
	} {
		_, err := Parse(strings.NewReader(body), all)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%s: want *plan.Error, got %v", name, err)
		}
	}
}

func TestResolveCarriesArgs(t *testing.T) {
	args := map[string]string{"reviewer": "alice"}
	tasks, err := Resolve(snapshot(), Items("request-review", []string{"acme/api#1", "acme/api#99"}, args))
	if err != nil {
		t.Fatal(err)
	}
	if !cmp.Equal(tasks[0].Args, args) || !cmp.Equal(tasks[1].Result.Args, args) {
		t.Errorf("args not carried: task=%v preResult=%v", tasks[0].Args, tasks[1].Result.Args)
	}
}
