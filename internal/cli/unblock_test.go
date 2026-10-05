package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

const renovateBody = "Update react.\n\n - [ ] <!-- rebase-check -->If you want to rebase/retry this PR, check this box\n"

// supersededFake is sampleFake plus acme/api#5, a newer lodash PR that
// supersedes acme/api#1.
func supersededFake() *githubtest.Fake {
	f := sampleFake()
	f.Add(githubtest.NewPR("acme/api", 5, "Bump lodash from 4.17.20 to 4.17.22"), true, false)
	return f
}

func TestUnblockDryRuns(t *testing.T) {
	tests := []struct {
		args []string
		want string
		code int
	}{
		{[]string{"rebase", "acme/api#3"}, "acme/api#3 planned", ExitOK},
		{[]string{"recreate", "acme/api#3"}, "acme/api#3 planned", ExitOK},
		{[]string{"rerun", "acme/web#2"}, "acme/web#2 planned", ExitOK},
		{[]string{"request-review", "acme/api#1", "--reviewer", "alice"}, "acme/api#1 planned", ExitOK},
		{[]string{"close", "acme/api#3", "--reason", "superseded"}, "acme/api#3 denied blocker_missing", ExitDenied},
	}
	for _, tt := range tests {
		t.Run(tt.args[0], func(t *testing.T) {
			f := sampleFake()
			code, out, _ := runApp(t, testApp(f), append(tt.args, "--json")...)
			e := decodeResults(t, out)
			if diff := cmp.Diff([]string{tt.want}, summary(e.Data.Results)); diff != "" || code != tt.code || !e.DryRun || len(f.Calls) != 0 {
				t.Errorf("code=%d calls=%v (-want +got):\n%s", code, f.Calls, diff)
			}
		})
	}
}

func TestRequestReviewDryRunGolden(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "request-review", "acme/api#1", "--reviewer", "acme/platform", "--json")
	assertGolden(t, "request_review_dry.json", out)
}

func TestUnblockYes(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		setup func(*githubtest.Fake)
		steps []string
		calls []string
	}{
		{"renovate rebase", []string{"rebase", "acme/api#3"}, func(f *githubtest.Fake) { f.PRs["acme/api#3"].Body = renovateBody },
			[]string{"requested rebase"}, []string{"body acme/api#3"}},
		{"dependabot recreate", []string{"recreate", "acme/web#2"}, func(*githubtest.Fake) {},
			[]string{"requested recreate"}, []string{"comment acme/web#2 @dependabot recreate"}},
		{"rerun", []string{"rerun", "acme/web#2"}, func(f *githubtest.Fake) { f.PRs["acme/web#2"].CheckRuns[0].WorkflowRunID = 9 },
			[]string{"re-ran workflow run 9"}, []string{"rerun acme/web 9"}},
		{"request-review", []string{"request-review", "acme/api#1", "--reviewer", "alice"}, func(f *githubtest.Fake) { f.Reviewers["alice"] = "U_1" },
			[]string{"requested review from alice"}, []string{"request-review acme/api#1 U_1"}},
		{"close", []string{"close", "acme/api#1", "--reason", "superseded"}, func(*githubtest.Fake) {},
			[]string{"commented", "closed"}, []string{"comment acme/api#1 Closed by gh dep-triage: Superseded by acme/api#5 (4.17.22).", "close acme/api#1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := supersededFake()
			tt.setup(f)
			code, out, _ := runApp(t, testApp(f), append(tt.args, "--yes", "--json")...)
			e := decodeResults(t, out)
			if code != ExitOK || len(e.Data.Results) != 1 || !cmp.Equal(e.Data.Results[0].Steps, tt.steps) {
				t.Fatalf("code=%d results=%+v", code, e.Data.Results)
			}
			if diff := cmp.Diff(tt.calls, f.Calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUnblockArgErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"missing reviewer":          {"request-review", "acme/api#1"},
		"bad reviewer":              {"request-review", "acme/api#1", "--reviewer", "a/b/c"},
		"missing reason":            {"close", "acme/api#1"},
		"bad reason":                {"close", "acme/api#1", "--reason", "bored"},
		"allow-major not on rebase": {"rebase", "acme/api#3", "--allow-major"},
		"no refs":                   {"rerun"},
	} {
		f := sampleFake()
		code, out, _ := runApp(t, testApp(f), append(args, "--json")...)
		e := decodeResults(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" || len(f.Calls) != 0 ||
			strings.Contains(e.Errors[0].Message, "unknown command") {
			t.Errorf("%s: code=%d errors=%+v", name, code, e.Errors)
		}
	}
}

func TestApplyUnblockActions(t *testing.T) {
	f := supersededFake()
	a := testApp(f)
	a.stdin = strings.NewReader(`[{"action":"close","ref":"acme/api#1","args":{"reason":"superseded"}},{"action":"rebase","ref":"acme/api#3"}]`)
	code, out, _ := runApp(t, a, "apply", "--plan", "-", "--json")
	e := decodeResults(t, out)
	if diff := cmp.Diff([]string{"acme/api#1 planned", "acme/api#3 planned"}, summary(e.Data.Results)); diff != "" || code != ExitOK {
		t.Errorf("code=%d (-want +got):\n%s", code, diff)
	}
	if !cmp.Equal(e.Data.Results[0].Args, map[string]string{"reason": "superseded"}) {
		t.Errorf("args = %v", e.Data.Results[0].Args)
	}
}

func TestHumanResultShowsArgs(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "request-review", "acme/api#1", "--reviewer", "alice")
	if !strings.Contains(out, "request-review (alice)") {
		t.Errorf("table missing action label:\n%s", out)
	}
}

// blockerFake has one PR per blocker kind, so list suggests every action.
func blockerFake() *githubtest.Fake {
	f := githubtest.NewFake("octocat")
	add := func(n int, title string, mod func(*model.PR)) {
		pr := githubtest.NewPR("acme/api", n, title)
		mod(pr)
		f.Add(pr, true, false)
	}
	add(1, "Bump a from 1.0.0 to 1.0.1", func(p *model.PR) { p.MergeStateStatus = "BEHIND" })
	add(2, "Bump b from 1.0.0 to 1.0.1", func(p *model.PR) { p.MergeStateStatus = "DIRTY" })
	add(3, "Bump c from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow.Add(-time.Hour)}}
	})
	add(4, "Bump d from 1.0.0 to 1.0.1", func(*model.PR) {})
	add(5, "Bump d from 1.0.0 to 1.0.2", func(*model.PR) {})
	add(6, "Bump e from 1.0.0 to 1.0.1", func(p *model.PR) { p.CreatedAt = testNow.Add(-40 * 24 * time.Hour) })
	add(7, "Bump f from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.CheckRuns = []model.Check{githubtest.CheckRun("lint", "COMPLETED", "TIMED_OUT", 71), githubtest.CheckRun("test", "COMPLETED", "FAILURE", 72)}
	})
	f.Logs[71], f.Logs[72] = "timed out\n", "FAIL\n"
	f.Reviewers["alice"] = "U_1"
	return f
}

// Every suggested command must be a valid invocation of this binary.
func TestSuggestedCommandsAreValid(t *testing.T) {
	_, out, _ := runApp(t, testApp(blockerFake()), "list", "--json")
	var env struct {
		Data struct {
			Groups []struct {
				PRs []model.PR `json:"prs"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	var commands []string
	for _, g := range env.Data.Groups {
		for _, pr := range g.PRs {
			for _, b := range pr.Blockers {
				for _, s := range b.SuggestedActions {
					actions[s.Action] = true
					commands = append(commands, s.Command)
				}
			}
		}
	}
	for _, want := range []string{"rebase", "recreate", "rerun", "fix", "request-review", "close"} {
		if !actions[want] {
			t.Errorf("fixture suggests no %q action; got %v", want, actions)
		}
	}
	for _, c := range commands {
		fields := strings.Fields(strings.Replace(c, "REVIEWER", "alice", 1))
		if len(fields) < 3 || fields[0] != "gh" || fields[1] != "dep-triage" {
			t.Errorf("not a gh dep-triage command: %q", c)
			continue
		}
		code, out, _ := runApp(t, testApp(blockerFake()), append(fields[2:], "--json")...)
		var e struct{ Errors []model.Problem }
		if err := json.Unmarshal([]byte(out), &e); err != nil || code == ExitError || len(e.Errors) > 0 {
			t.Errorf("%q: code=%d errors=%+v err=%v", c, code, e.Errors, err)
		}
	}
}
