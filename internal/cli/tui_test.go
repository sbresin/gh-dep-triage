package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/tui"
)

type tuiCall struct {
	snap *model.Snapshot
	deps tui.Deps
}

func tuiApp(f *githubtest.Fake, code int, summary ...string) (*app, *[]tuiCall) {
	a := testApp(f)
	calls := &[]tuiCall{}
	a.isTTY = func() bool { return true }
	a.runTUI = func(_ context.Context, s *model.Snapshot, d tui.Deps) (int, []string, error) {
		*calls = append(*calls, tuiCall{s, d})
		return code, summary, nil
	}
	return a, calls
}

func TestRootWithoutTTYPrintsHelp(t *testing.T) {
	f := sampleFake()
	code, out, _ := runApp(t, testApp(f))
	if code != ExitOK || !strings.Contains(out, "Usage:") || len(f.Queries) != 0 {
		t.Errorf("code=%d queries=%v out=%q", code, f.Queries, out)
	}
}

func TestRootJSONPrintsHelp(t *testing.T) {
	a, calls := tuiApp(sampleFake(), 0)
	code, out, _ := runApp(t, a, "--json")
	if code != ExitOK || !strings.Contains(out, "Usage:") || len(*calls) != 0 {
		t.Errorf("code=%d calls=%d", code, len(*calls))
	}
}

func TestRootWithTTYStartsTUI(t *testing.T) {
	f := sampleFake()
	a, calls := tuiApp(f, 0)
	code, _, errOut := runApp(t, a)
	if code != ExitOK || len(*calls) != 1 {
		t.Fatalf("code=%d calls=%d", code, len(*calls))
	}
	c := (*calls)[0]
	if len(c.snap.PRs()) != 4 || c.deps.Who != "user octocat" || c.deps.Rules.Soft {
		t.Errorf("snap=%d who=%q rules=%+v", len(c.snap.PRs()), c.deps.Who, c.deps.Rules)
	}
	if !strings.Contains(errOut, "Searching for dependency PRs") {
		t.Errorf("initial load reports progress on stderr: %q", errOut)
	}

	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	q := c.deps.Queue
	if q == nil {
		t.Fatal("the TUI gets a work queue")
	}
	if _, err := q.Submit(model.ActionMerge, c.snap.Find(model.PRRef{Repo: "acme/api", Number: 1}), nil); err != nil {
		t.Fatal(err)
	}
	for ev := range q.Events() {
		if ev.Kind == executor.EventFinished {
			if ev.Job.Result.Status != model.ResultSuccess {
				t.Errorf("result %+v", ev.Job.Result)
			}
			break
		}
	}
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1", "merge acme/api#1 sha1 SQUASH"}, f.Calls); diff != "" {
		t.Errorf("queue wiring (-want +got):\n%s", diff)
	}
	q.Stop(false)
	for range q.Events() {
	}
}

func TestTUIReloadIsQuiet(t *testing.T) {
	a, calls := tuiApp(sampleFake(), 0)
	var errBuf strings.Builder
	runApp(t, a)
	a.stderr = &errBuf
	if _, err := (*calls)[0].deps.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("reload must not write to stderr (it would corrupt the TUI): %q", errBuf.String())
	}
}

func TestRootTUITeamAndExitCode(t *testing.T) {
	a, calls := tuiApp(sampleFake(), 130)
	code, _, _ := runApp(t, a, "--team", "acme/platform")
	if code != 130 || (*calls)[0].deps.Who != "team acme/platform" {
		t.Errorf("code=%d who=%q", code, (*calls)[0].deps.Who)
	}
}

func TestRootTUIPrintsSummary(t *testing.T) {
	a, _ := tuiApp(sampleFake(), 130, "success acme/api#1: approved, merged (squash)", "skipped acme/web#2: cancelled before it started")
	code, out, errOut := runApp(t, a)
	want := "success acme/api#1: approved, merged (squash)\nskipped acme/web#2: cancelled before it started\n"
	if code != 130 || out != "" || !strings.HasSuffix(errOut, want) {
		t.Errorf("code=%d out=%q stderr=%q, want suffix %q", code, out, errOut, want)
	}
	a, _ = tuiApp(sampleFake(), 0, "success acme/api#1: approved")
	if _, _, errOut := runApp(t, a); !strings.HasSuffix(errOut, "success acme/api#1: approved\n") {
		t.Errorf("the summary is printed after a normal exit too: %q", errOut)
	}
}

func TestRootTUINoPRs(t *testing.T) {
	a, calls := tuiApp(githubtest.NewFake("octocat"), 0)
	code, _, errOut := runApp(t, a)
	if code != ExitOK || len(*calls) != 0 || !strings.Contains(errOut, "No matching Dependabot or Renovate PRs found.") {
		t.Errorf("code=%d calls=%d stderr=%q", code, len(*calls), errOut)
	}
}
