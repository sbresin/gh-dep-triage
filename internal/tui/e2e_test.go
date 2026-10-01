package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// harness runs a Model like Bubble Tea does: commands run in goroutines and
// their messages are fed back to Update on the test goroutine.
type harness struct {
	t    *testing.T
	m    Model
	msgs chan tea.Msg
}

func newHarness(t *testing.T, m Model) *harness {
	h := &harness{t: t, m: m, msgs: make(chan tea.Msg, 64)}
	h.exec(m.Init())
	return h
}

func (h *harness) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		switch msg := cmd().(type) {
		case nil:
		case tea.BatchMsg:
			for _, c := range msg {
				h.exec(c)
			}
		default:
			h.msgs <- msg
		}
	}()
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		nm, cmd := h.m.Update(key(k))
		h.m = nm.(Model)
		h.exec(cmd)
	}
}

// untilQuit feeds messages until the model quits.
func (h *harness) untilQuit() {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if _, ok := msg.(tea.QuitMsg); ok {
				return
			}
			nm, cmd := h.m.Update(msg)
			h.m = nm.(Model)
			h.exec(cmd)
		case <-deadline:
			h.t.Fatalf("the model did not quit; status %q", h.m.status)
		}
	}
}

// gatedClient blocks the first mutation (approve or merge) until released.
type gatedClient struct {
	*githubtest.Fake
	once             sync.Once
	started, release chan struct{}
}

func newGated(prs ...*model.PR) *gatedClient {
	f := githubtest.NewFake("octocat")
	for _, pr := range prs {
		f.Add(pr, true, false)
	}
	return &gatedClient{Fake: f, started: make(chan struct{}), release: make(chan struct{})}
}

func (g *gatedClient) gate() {
	first := false
	g.once.Do(func() { first = true })
	if first {
		close(g.started)
		<-g.release
	}
}

func (g *gatedClient) Approve(ctx context.Context, id, head string) error {
	g.gate()
	return g.Fake.Approve(ctx, id, head)
}

func (g *gatedClient) Merge(ctx context.Context, id, head, method string) error {
	g.gate()
	return g.Fake.Merge(ctx, id, head, method)
}

func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func waitStarted(t *testing.T, g *gatedClient) {
	t.Helper()
	select {
	case <-g.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no mutation started")
	}
}

// The user keeps triaging while the first merge is in flight, then waits.
func TestEndToEndTriageWhileMerging(t *testing.T) {
	prs := func() []*model.PR {
		return []*model.PR{
			mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
			mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
			mkPR("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0", func(p *model.PR) { p.MergeStateStatus = "BEHIND" }),
		}
	}
	g := newGated(prs()...)
	for _, ref := range []string{"acme/api#1", "acme/web#2"} {
		g.AfterApprove[ref] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	}
	q := executor.NewQueue(context.Background(), g, executor.Options{Viewer: "octocat", Sleep: noSleep})
	h := newHarness(t, newTest(snapshot(prs()...), Deps{Queue: q, Browse: func(string) error { return nil }}))

	h.press("j", "space", "c", "y") // rows: axios (api#3), lodash group
	waitStarted(t, g)
	h.press("k", "b") // triage on: blockers of api#3
	if h.m.screen != screenPager || h.m.pagerTitle != "Blockers: acme/api#3" {
		t.Fatalf("triage while merging: screen=%d title=%q", h.m.screen, h.m.pagerTitle)
	}
	h.press("esc", "q")
	if h.m.popup != popupQuit {
		t.Fatalf("q with pending work opens the quit popup; status %q", h.m.status)
	}
	h.press("w")
	close(g.release)
	h.untilQuit()

	want := []string{"success acme/api#1: approved, merged (squash)", "success acme/web#2: approved, merged (squash)"}
	got := h.m.Summary()
	if !cmp.Equal(want, got) && !cmp.Equal([]string{want[1], want[0]}, got) {
		t.Errorf("summary = %v", got)
	}
	if h.m.Interrupted() {
		t.Error("waiting is not an interrupt")
	}
}

// Ctrl-C mid-run: the in-flight job finishes, the queued one is cancelled.
func TestEndToEndCtrlC(t *testing.T) {
	approved := func(p *model.PR) {
		p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
		p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow}}
	}
	for _, tt := range []struct {
		name      string
		mods      []func(*model.PR)
		wantFirst string
		wantCalls []string
	}{
		{"merge in flight", []func(*model.PR){approved}, "success acme/api#1: merged (squash)",
			[]string{"merge acme/api#1 sha1 SQUASH"}},
		{"approve in flight", nil, "skipped acme/api#1: cancelled before merging",
			[]string{"approve acme/api#1 sha1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prs := func() []*model.PR {
				return []*model.PR{
					mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", tt.mods...),
					mkPR("acme/api", 5, "Bump axios from 1.6.0 to 1.7.0", tt.mods...),
				}
			}
			g := newGated(prs()...)
			q := executor.NewQueue(context.Background(), g, executor.Options{Viewer: "octocat", Sleep: noSleep})
			h := newHarness(t, newTest(snapshot(prs()...), Deps{Queue: q, Browse: func(string) error { return nil }}))
			h.press("space", "j", "space", "c", "y") // rows: axios (api#5), lodash (api#1)
			waitStarted(t, g)
			h.press("ctrl+c")
			close(g.release)
			h.untilQuit()

			want := []string{"skipped acme/api#5: cancelled before it started", tt.wantFirst}
			if diff := cmp.Diff(want, h.m.Summary()); diff != "" {
				t.Errorf("summary (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, g.Calls); diff != "" {
				t.Errorf("mutations (-want +got):\n%s", diff)
			}
			if !h.m.Interrupted() {
				t.Error("ctrl+c exits with 130")
			}
		})
	}
}
