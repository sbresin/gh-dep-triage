package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/policy"
)

// fakeRun records executed refs and reports success (or cancelled once ctx is done).
type fakeRun struct {
	mu       sync.Mutex
	fresh    *model.Snapshot
	loadErr  error
	executed []string
}

func (f *fakeRun) deps() Deps {
	return Deps{
		Load: func(context.Context) (*model.Snapshot, error) { return f.fresh, f.loadErr },
		Execute: func(ctx context.Context, tasks []plan.Task, onResult func(model.Result)) []model.Result {
			for _, t := range tasks {
				r := model.Result{Action: t.Action, Ref: t.PR.Ref, Steps: []string{}}
				if ctx.Err() != nil {
					r.Status, r.Reason = model.ResultSkipped, model.ReasonCancelled
				} else {
					f.mu.Lock()
					f.executed = append(f.executed, t.PR.Ref)
					f.mu.Unlock()
					r.Status, r.Steps = model.ResultSuccess, []string{"approved", "merged (squash)"}
				}
				onResult(r)
			}
			return nil
		},
		Browse: func(string) error { return nil },
	}
}

// drive feeds msg and keeps running returned commands until none remain.
func drive(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	queue := []tea.Msg{msg}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			t.Fatal("too many steps")
		}
		next := queue[0]
		queue = queue[1:]
		nm, cmd := m.Update(next)
		m = nm.(Model)
		if cmd == nil {
			continue
		}
		switch out := cmd().(type) {
		case nil:
		case tea.QuitMsg:
			return m
		case tea.BatchMsg:
			for _, c := range out {
				if c != nil {
					if o := c(); o != nil {
						queue = append(queue, o)
					}
				}
			}
		default:
			queue = append(queue, out)
		}
	}
	return m
}

func resultSummary(rs []model.Result) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, strings.TrimSpace(r.Ref+" "+r.Status+" "+r.Reason))
	}
	return out
}

// selectLodash selects the two-PR lodash group (third row) and opens confirm.
func selectLodash(m Model) Model {
	m, _ = press(m, "j", "j", "space", "c")
	return m
}

func TestConfirmScreen(t *testing.T) {
	m := selectLodash(newTest(fixture(), Deps{}))
	if m.screen != screenConfirm {
		t.Fatalf("screen = %d", m.screen)
	}
	got := plain(m)
	for _, want := range []string{"Confirm planned actions", "2 PRs selected. Enter executes. q returns to triage.",
		"acme/api", "#1  approve+merge  lodash -> 4.17.21 [patch]", "acme/web", "#2  approve+merge"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm missing %q:\n%s", want, got)
		}
	}
	m, _ = press(m, "esc")
	if m.screen != screenList || m.selectedCount() != 2 {
		t.Errorf("esc keeps selection: screen=%d selected=%d", m.screen, m.selectedCount())
	}
}

func TestConfirmShowsHardRuleDenial(t *testing.T) {
	m := selectLodash(newTest(fixture(), Deps{Rules: policy.Rules{Bots: []string{"renovate"}}}))
	if !strings.Contains(plain(m), "#1  denied (not_bot_pr)") {
		t.Errorf("denial not shown:\n%s", plain(m))
	}
}

func TestExecutionHappyPath(t *testing.T) {
	f := &fakeRun{fresh: fixture()}
	m := selectLodash(newTest(fixture(), f.deps()))
	m = drive(t, m, key("enter"))
	if m.screen != screenProgress || m.phase != phaseDone {
		t.Fatalf("screen=%d phase=%d", m.screen, m.phase)
	}
	if diff := cmp.Diff([]string{"acme/api#1", "acme/web#2"}, f.executed); diff != "" {
		t.Errorf("executed (-want +got):\n%s", diff)
	}
	if got := plain(m); !strings.Contains(got, "Done 2/2") || !strings.Contains(got, "approved, merged (squash)") {
		t.Errorf("progress view:\n%s", got)
	}
	m, _ = press(m, "x")
	if m.screen != screenResults || !strings.Contains(plain(m), "Succeeded (2)") || !strings.Contains(plain(m), "Failures (0)") {
		t.Errorf("results:\n%s", plain(m))
	}
	if _, cmd := press(m, "q"); !isQuit(cmd) {
		t.Error("q quits from results")
	}
}

func TestExecutionUsesDisplayedHead(t *testing.T) {
	fresh := snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) { p.HeadOid = "sha-new" }),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
	)
	f := &fakeRun{fresh: fresh}
	m := drive(t, selectLodash(newTest(fixture(), f.deps())), key("enter"))
	if diff := cmp.Diff([]string{"acme/api#1 failed head_changed", "acme/web#2 success"}, resultSummary(m.results)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"acme/web#2"}, f.executed); diff != "" {
		t.Errorf("only the unchanged PR may run (-want +got):\n%s", diff)
	}
}

func TestExecutionDeniesPRThatNowFails(t *testing.T) {
	fresh := snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) {
			p.CheckRuns = []model.Check{{Name: "t", Kind: model.CheckKindRun, Status: "COMPLETED", Conclusion: "FAILURE"}}
		}),
	)
	f := &fakeRun{fresh: fresh}
	m := drive(t, selectLodash(newTest(fixture(), f.deps())), key("enter"))
	if diff := cmp.Diff([]string{"acme/web#2 denied checks_failing", "acme/api#1 success"}, resultSummary(m.results)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestRefreshFailure(t *testing.T) {
	f := &fakeRun{loadErr: errors.New("HTTP 502")}
	m := drive(t, selectLodash(newTest(fixture(), f.deps())), key("enter"))
	if m.phase != phaseDone || len(f.executed) != 0 || !strings.Contains(plain(m), "Refresh failed: HTTP 502") {
		t.Errorf("phase=%d executed=%v view:\n%s", m.phase, f.executed, plain(m))
	}
}

func TestCtrlCDuringExecution(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := &fakeRun{fresh: fixture()}
	deps := f.deps()
	inner := deps.Execute
	deps.Execute = func(ctx context.Context, tasks []plan.Task, onResult func(model.Result)) []model.Result {
		close(started)
		<-release                                                     // the first PR's mutation is "in flight" until released
		return inner(context.WithoutCancel(ctx), tasks[:1], onResult) // in-flight one completes
	}
	m := selectLodash(newTest(fixture(), deps))
	nm, loadCmd := m.Update(key("enter"))
	m = nm.(Model)
	nm, startCmd := m.Update(loadCmd())
	m = nm.(Model)
	if m.phase != phaseExecuting {
		t.Fatalf("phase = %d", m.phase)
	}
	first := make(chan tea.Msg, 1)
	go func() { first <- startCmd() }()
	<-started

	m, _ = press(m, "ctrl+c")
	if !m.cancelling || m.runCtx.Err() == nil || m.Interrupted() {
		t.Fatalf("first ctrl+c cancels: cancelling=%v ctxErr=%v interrupted=%v", m.cancelling, m.runCtx.Err(), m.Interrupted())
	}
	if !strings.Contains(plain(m), "press ctrl+c again to quit") {
		t.Errorf("footer:\n%s", plain(m))
	}
	close(release)
	m = drive(t, m, <-first)
	if m.phase != phaseDone || resultSummary(m.results)[0] != "acme/api#1 success" {
		t.Errorf("in-flight PR finishes: phase=%d results=%v", m.phase, resultSummary(m.results))
	}

	// A second ctrl+c while still executing quits immediately.
	m2 := selectLodash(newTest(fixture(), (&fakeRun{fresh: fixture()}).deps()))
	nm, loadCmd = m2.Update(key("enter"))
	nm, _ = nm.(Model).Update(loadCmd())
	m2 = nm.(Model)
	m2, _ = press(m2, "ctrl+c")
	m2, cmd := press(m2, "ctrl+c")
	if !isQuit(cmd) || !m2.Interrupted() {
		t.Error("second ctrl+c quits as interrupted")
	}
}

func TestTinyTerminalMidRunIgnoresQuit(t *testing.T) {
	m := selectLodash(newTest(fixture(), (&fakeRun{fresh: fixture()}).deps()))
	nm, loadCmd := m.Update(key("enter"))
	nm, _ = nm.(Model).Update(loadCmd())
	nm, _ = nm.(Model).Update(tea.WindowSizeMsg{Width: 30, Height: 5})
	m = nm.(Model)
	if m.phase != phaseExecuting {
		t.Fatalf("phase = %d", m.phase)
	}
	for _, k := range []string{"q", "esc"} {
		if nm, cmd := press(m, k); isQuit(cmd) || nm.phase != phaseExecuting || nm.screen != screenProgress {
			t.Errorf("%s mid-run on a tiny terminal must not quit: phase=%d screen=%d", k, nm.phase, nm.screen)
		}
	}
	m, cmd := press(m, "ctrl+c")
	if isQuit(cmd) || !m.cancelling || m.runCtx.Err() == nil {
		t.Errorf("ctrl+c cancels: cancelling=%v ctxErr=%v", m.cancelling, m.runCtx.Err())
	}
}

func TestResultsSections(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m.results = []model.Result{
		{Ref: "acme/api#1", Status: model.ResultSuccess, Steps: []string{"approved", "merged (squash)"}},
		{Ref: "acme/web#2", Status: model.ResultFailed, Reason: model.ReasonMutationFailed, Message: "boom", Steps: []string{"approved"}},
		{Ref: "acme/api#3", Status: model.ResultDenied, Reason: model.ReasonChecksFailing, Message: "1 failing check(s): t", Steps: []string{}},
	}
	m = m.showResults()
	got := plain(m)
	for _, want := range []string{"Failures (1)", "acme/web#2  mutation_failed: approved: boom", "Denied (1)",
		"Skipped (0)", "none", "Succeeded (1)", "acme/api#1  approved, merged (squash)"} {
		if !strings.Contains(got, want) {
			t.Errorf("results missing %q:\n%s", want, got)
		}
	}
	if m.list.focusedRef() != "acme/web#2" {
		t.Errorf("cursor starts on first item, got %q", m.list.focusedRef())
	}
	m, _ = press(m, "d")
	if m.screen != screenPager || m.pagerReturn != screenResults {
		t.Errorf("d on results opens pager returning to results: screen=%d return=%d", m.screen, m.pagerReturn)
	}
}
