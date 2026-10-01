package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/tui"
)

type tuiCall struct {
	snap *model.Snapshot
	deps tui.Deps
}

func tuiApp(f *githubtest.Fake, code int) (*app, *[]tuiCall) {
	a := testApp(f)
	calls := &[]tuiCall{}
	a.isTTY = func() bool { return true }
	a.runTUI = func(_ context.Context, s *model.Snapshot, d tui.Deps) (int, error) {
		*calls = append(*calls, tuiCall{s, d})
		return code, nil
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
	var streamed []string
	pr := c.snap.Find(model.PRRef{Repo: "acme/api", Number: 1})
	c.deps.Execute(context.Background(), []plan.Task{{Action: model.ActionMerge, PR: pr}},
		func(r model.Result) { streamed = append(streamed, r.Ref+" "+r.Status) })
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1", "merge acme/api#1 sha1 SQUASH"}, f.Calls); diff != "" {
		t.Errorf("execute wiring (-want +got):\n%s", diff)
	}
	if !cmp.Equal(streamed, []string{"acme/api#1 success"}) {
		t.Errorf("streamed = %v", streamed)
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
	code, _, _ := runApp(t, a, "--team", "platform")
	if code != 130 || (*calls)[0].deps.Who != "team platform" {
		t.Errorf("code=%d who=%q", code, (*calls)[0].deps.Who)
	}
}

func TestRootTUINoPRs(t *testing.T) {
	a, calls := tuiApp(githubtest.NewFake("octocat"), 0)
	code, _, errOut := runApp(t, a)
	if code != ExitOK || len(*calls) != 0 || !strings.Contains(errOut, "No matching Dependabot or Renovate PRs found.") {
		t.Errorf("code=%d calls=%d stderr=%q", code, len(*calls), errOut)
	}
}
