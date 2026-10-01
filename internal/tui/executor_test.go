package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
)

// gatedClient blocks the first mutation (approve or merge) until released.
type gatedClient struct {
	*githubtest.Fake
	once             sync.Once
	started, release chan struct{}
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

func TestCtrlCWithRealExecutor(t *testing.T) {
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
		// The in-flight merge finishes; the queued PR in the same repo is skipped.
		{"merge in flight", []func(*model.PR){approved}, "acme/api#1 success  [merged (squash)]",
			[]string{"merge acme/api#1 sha1 SQUASH"}},
		// The in-flight approval finishes, but the merge after it is not started.
		{"approve in flight", nil, "acme/api#1 skipped cancelled [approved]",
			[]string{"approve acme/api#1 sha1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prs := func() []*model.PR {
				return []*model.PR{
					mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", tt.mods...),
					mkPR("acme/api", 5, "Bump axios from 1.6.0 to 1.7.0", tt.mods...),
				}
			}
			fake := githubtest.NewFake("octocat")
			for _, pr := range prs() {
				fake.Add(pr, true, false)
			}
			client := &gatedClient{Fake: fake, started: make(chan struct{}), release: make(chan struct{})}
			deps := Deps{
				Load: func(context.Context) (*model.Snapshot, error) { return snapshot(prs()...), nil },
				Execute: func(ctx context.Context, tasks []plan.Task, onResult func(model.Result)) []model.Result {
					return executor.Run(ctx, client, tasks, executor.Options{Viewer: "octocat", OnResult: onResult,
						Sleep: func(context.Context, time.Duration) error { return nil }})
				},
				Browse: func(string) error { return nil },
			}
			m, _ := press(newTest(snapshot(prs()...), deps), "space", "j", "space", "c")
			if len(m.confirmPRs) != 2 {
				t.Fatalf("confirm has %d PRs", len(m.confirmPRs))
			}
			nm, loadCmd := m.Update(key("enter"))
			nm, startCmd := nm.(Model).Update(loadCmd())
			m = nm.(Model)
			first := make(chan tea.Msg, 1)
			go func() { first <- startCmd() }()
			<-client.started

			m, _ = press(m, "ctrl+c")
			close(client.release)
			m = drive(t, m, <-first)

			if m.phase != phaseDone {
				t.Fatalf("phase = %d", m.phase)
			}
			got := []string{}
			for _, r := range m.results {
				got = append(got, r.Ref+" "+r.Status+" "+r.Reason+" ["+strings.Join(r.Steps, ", ")+"]")
			}
			want := []string{tt.wantFirst, "acme/api#5 skipped cancelled []"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("results (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, fake.Calls); diff != "" {
				t.Errorf("mutations (-want +got):\n%s", diff)
			}
		})
	}
}
