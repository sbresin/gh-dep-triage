package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

const renovateBody = "Update react.\n\n - [ ] <!-- rebase-check -->If you want to rebase/retry this PR, check this box\n"

func runJob(t *testing.T, f *githubtest.Fake, j Job) model.Result {
	t.Helper()
	got, _ := runAll(t, context.Background(), f, opts(&sleeps{}), j)
	return got[0]
}

func argJob(f *githubtest.Fake, action string, args map[string]string) Job {
	j := job(f, action, "acme/api#1")
	j.Args = args
	return j
}

func renovate(p *model.PR) { p.Author = "renovate" }

func TestBotRequest(t *testing.T) {
	tests := []struct {
		name, action string
		mod          func(*model.PR)
		want         string // status reason
		steps        []string
		calls        []string
		body         string // expected stored body, "" = don't check
	}{
		{"dependabot rebase", "rebase", func(*model.PR) {}, "success ", []string{"requested rebase"},
			[]string{"comment acme/api#1 @dependabot rebase"}, ""},
		{"dependabot recreate", "recreate", func(*model.PR) {}, "success ", []string{"requested recreate"},
			[]string{"comment acme/api#1 @dependabot recreate"}, ""},
		{"renovate ticks box", "rebase", func(p *model.PR) { renovate(p); p.Body = renovateBody }, "success ", []string{"requested rebase"},
			[]string{"body acme/api#1"}, strings.Replace(renovateBody, "- [ ]", "- [x]", 1)},
		{"renovate recreate ticks box", "recreate", func(p *model.PR) { renovate(p); p.Body = renovateBody }, "success ", []string{"requested recreate"},
			[]string{"body acme/api#1"}, ""},
		{"renovate already ticked", "rebase", func(p *model.PR) { renovate(p); p.Body = strings.Replace(renovateBody, "[ ]", "[x]", 1) },
			"skipped already_requested", []string{}, nil, ""},
		{"renovate already ticked upper", "rebase", func(p *model.PR) { renovate(p); p.Body = strings.Replace(renovateBody, "[ ]", "[X]", 1) },
			"skipped already_requested", []string{}, nil, ""},
		{"renovate without checkbox", "rebase", func(p *model.PR) { renovate(p); p.Body = "no box" },
			"failed no_rebase_checkbox", []string{}, nil, ""},
		{"other bot", "rebase", func(p *model.PR) { p.Author = "snyk-bot" }, "failed unsupported_bot", []string{}, nil, ""},
		{"self-hosted renovate", "rebase", func(p *model.PR) { p.Author = "snyk-bot"; p.Body = renovateBody }, "success ", []string{"requested rebase"},
			[]string{"body acme/api#1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake(tt.mod)
			o := opts(&sleeps{})
			if tt.name == "other bot" || tt.name == "self-hosted renovate" {
				o.Rules.Bots = []string{"snyk-bot"}
			}
			got, _ := runAll(t, context.Background(), f, o, job(f, tt.action, "acme/api#1"))
			r := got[0]
			if st := r.Status + " " + r.Reason; st != tt.want || !cmp.Equal(r.Steps, tt.steps) {
				t.Errorf("got %+v, want %q steps %v", r, tt.want, tt.steps)
			}
			if diff := cmp.Diff(tt.calls, f.Calls); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}
			if tt.body != "" && f.PRs["acme/api#1"].Body != tt.body {
				t.Errorf("body = %q", f.PRs["acme/api#1"].Body)
			}
		})
	}
}

func failedRun(name, conclusion string, jobID, runID int64) model.Check {
	c := githubtest.CheckRun(name, "COMPLETED", conclusion, jobID)
	c.WorkflowRunID = runID
	return c
}

func TestRerun(t *testing.T) {
	f := newMergeFake(func(p *model.PR) {
		p.CheckRuns = []model.Check{
			failedRun("test", "FAILURE", 1, 42), failedRun("lint", "TIMED_OUT", 2, 42), failedRun("e2e", "FAILURE", 3, 43),
			failedRun("ok", "SUCCESS", 4, 44),
			{Name: "ci/legacy", Kind: model.CheckKindStatus, Conclusion: "ERROR"},
		}
	})
	r := runJob(t, f, job(f, "rerun", "acme/api#1"))
	if r.Status != model.ResultSuccess || !cmp.Equal(r.Steps, []string{"re-ran workflow run 42", "re-ran workflow run 43"}) ||
		!strings.Contains(r.Message, "ci/legacy") {
		t.Errorf("got %+v", r)
	}
	if diff := cmp.Diff([]string{"rerun acme/api 42", "rerun acme/api 43"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestRerunSkipsAndFailures(t *testing.T) {
	tests := []struct {
		name  string
		mod   func(*model.PR)
		fail  string // MutationErr key
		want  string
		steps []string
	}{
		{"nothing failing", func(*model.PR) {}, "", "skipped no_failed_checks", []string{}},
		{"only non-actions", func(p *model.PR) {
			p.CheckRuns = []model.Check{{Name: "ci/legacy", Kind: model.CheckKindStatus, Conclusion: "ERROR"}}
		}, "", "skipped not_rerunnable", []string{}},
		{"second run fails", func(p *model.PR) {
			p.CheckRuns = []model.Check{failedRun("a", "FAILURE", 1, 42), failedRun("b", "FAILURE", 2, 43)}
		}, "rerun acme/api 43", "failed mutation_failed", []string{"re-ran workflow run 42"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newMergeFake(tt.mod)
			if tt.fail != "" {
				f.MutationErr[tt.fail] = errors.New("HTTP 403")
			}
			r := runJob(t, f, job(f, "rerun", "acme/api#1"))
			if st := r.Status + " " + r.Reason; st != tt.want || !cmp.Equal(r.Steps, tt.steps) {
				t.Errorf("got %+v, want %q steps %v", r, tt.want, tt.steps)
			}
		})
	}
}

func TestRequestReview(t *testing.T) {
	f := newMergeFake()
	f.Reviewers["acme/platform"] = "T_9"
	r := runJob(t, f, argJob(f, "request-review", map[string]string{"reviewer": "acme/platform"}))
	if r.Status != model.ResultSuccess || !cmp.Equal(r.Steps, []string{"requested review from acme/platform"}) ||
		!cmp.Equal(r.Args, map[string]string{"reviewer": "acme/platform"}) {
		t.Errorf("got %+v", r)
	}
	if diff := cmp.Diff([]string{"request-review acme/api#1 T_9"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}

	f = newMergeFake()
	r = runJob(t, f, argJob(f, "request-review", map[string]string{"reviewer": "ghost"}))
	if r.Status != model.ResultFailed || r.Reason != model.ReasonReviewerNotFound || len(f.Calls) != 0 {
		t.Errorf("unknown reviewer: %+v calls=%v", r, f.Calls)
	}
}

func supersededJob(f *githubtest.Fake) Job {
	j := argJob(f, "close", map[string]string{"reason": "superseded"})
	j.PR.Blockers = []model.Blocker{{Code: model.BlockerSuperseded, Detail: "Superseded by acme/api#9 (1.0.2)"}}
	return j
}

// The refetched PR has no blockers; the confirmed PR's blockers must be used.
func TestCloseCommentsThenCloses(t *testing.T) {
	f := newMergeFake()
	r := runJob(t, f, supersededJob(f))
	if r.Status != model.ResultSuccess || !cmp.Equal(r.Steps, []string{"commented", "closed"}) {
		t.Errorf("got %+v", r)
	}
	want := []string{"comment acme/api#1 Closed by gh dep-triage: Superseded by acme/api#9 (1.0.2).", "close acme/api#1"}
	if diff := cmp.Diff(want, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// Blocker details come from bot titles; an @ must not ping anyone.
func TestCloseCommentDefusesMentions(t *testing.T) {
	f := newMergeFake()
	j := supersededJob(f)
	j.PR.Blockers[0].Detail = "Superseded by acme/api#9 (@evil/team)"
	runJob(t, f, j)
	if len(f.Calls) == 0 || strings.Contains(f.Calls[0], "@evil") || !strings.Contains(f.Calls[0], "@\u200bevil/team") {
		t.Errorf("calls = %q", f.Calls)
	}
}

func TestCloseDeniedWithoutBlocker(t *testing.T) {
	f := newMergeFake()
	r := runJob(t, f, argJob(f, "close", map[string]string{"reason": "stale"}))
	if r.Status != model.ResultDenied || r.Reason != model.ReasonBlockerMissing || len(f.Calls) != 0 {
		t.Errorf("got %+v calls=%v", r, f.Calls)
	}
}

func TestClosePartialFailure(t *testing.T) {
	f := newMergeFake()
	f.MutationErr["close acme/api#1"] = errors.New("boom")
	r := runJob(t, f, supersededJob(f))
	if r.Status != model.ResultFailed || r.Reason != model.ReasonMutationFailed || !cmp.Equal(r.Steps, []string{"commented"}) {
		t.Errorf("got %+v", r)
	}
}

func TestUnblockCancelledBeforeMutation(t *testing.T) {
	f := newMergeFake()
	ctx, cancel := context.WithCancel(context.Background())
	q := NewQueue(ctx, f, opts(&sleeps{}))
	r := q.run(ctx, job(f, "rebase", "acme/api#1"), func(s string) {
		if s == "requesting rebase" {
			cancel()
		}
	})
	q.Close(true)
	if r.Status != model.ResultSkipped || r.Reason != model.ReasonCancelled || len(f.Calls) != 0 {
		t.Errorf("got %+v calls=%v", r, f.Calls)
	}
}
