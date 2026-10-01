package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestReloadPrunesSelection(t *testing.T) {
	fresh := snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0", func(p *model.PR) { p.MergeStateStatus = "BEHIND" }),
		mkPR("acme/api", 4, "Bump eslint from 8.57.0 to 9.1.0"),
	)
	fresh.Warnings = []model.Problem{{Code: "fetch_failed", Ref: "acme/web#2", Message: "HTTP 502"}}
	loads := 0
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { loads++; return fresh, nil }}
	m := newTest(fixture(), deps)
	m, _ = press(m, "j", "j", "space")
	m, cmd := press(m, "g")
	if !m.reloading || m.status != "Reloading..." || cmd == nil {
		t.Fatalf("reloading=%v status=%q", m.reloading, m.status)
	}
	again, againCmd := press(m, "g")
	if againCmd != nil || again.status != "Already reloading..." {
		t.Errorf("second g: status=%q cmd=%v", again.status, againCmd)
	}
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if diff := cmp.Diff(map[string]bool{"acme/api#1": true}, m.selected); diff != "" {
		t.Errorf("selection (-want +got):\n%s", diff)
	}
	if m.reloading || m.status != "Reloaded 3 PRs. 1 warning(s)." || len(m.snap.PRs()) != 3 || loads != 1 {
		t.Errorf("reloading=%v status=%q prs=%d loads=%d", m.reloading, m.status, len(m.snap.PRs()), loads)
	}
}

func TestReloadFailureKeepsSnapshot(t *testing.T) {
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return nil, errors.New("HTTP 502") }}
	m, cmd := press(newTest(fixture(), deps), "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if m.status != "Reload failed: HTTP 502" || len(m.snap.PRs()) != 4 || m.reloading {
		t.Errorf("status=%q prs=%d reloading=%v", m.status, len(m.snap.PRs()), m.reloading)
	}
}
