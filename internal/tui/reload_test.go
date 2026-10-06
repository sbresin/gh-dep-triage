package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
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

func TestReloadClearsSelectionWhoseHeadMoved(t *testing.T) {
	fresh := snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) { p.HeadOid = "sha-new" }),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
	)
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return fresh, nil }}
	m, cmd := press(newTest(fixture(), deps), "j", "j", "space", "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if diff := cmp.Diff(map[string]bool{"acme/web#2": true}, m.selected); diff != "" {
		t.Errorf("selection (-want +got):\n%s", diff)
	}
	if want := "Reloaded 2 PRs. 1 selection(s) cleared: head moved."; m.status != want {
		t.Errorf("status = %q, want %q", m.status, want)
	}
}

func TestReloadFromEmptyList(t *testing.T) {
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return fixture(), nil }}
	m, cmd := press(newTest(snapshot(), deps), "g")
	if cmd == nil || !m.reloading {
		t.Fatalf("g on empty list: reloading=%v cmd=%v", m.reloading, cmd)
	}
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if len(m.rows()) != 3 || !strings.Contains(plain(m), "eslint") {
		t.Errorf("rows=%d view:\n%s", len(m.rows()), plain(m))
	}
}

func TestReloadClampsCursor(t *testing.T) {
	fresh := snapshot(mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"))
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return fresh, nil }}
	m, _ := press(newTest(fixture(), deps), "j", "j")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}
	m, cmd := press(m, "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if m.cursor != 0 || m.scroll != 0 || m.status != "Reloaded 1 PRs." {
		t.Errorf("cursor=%d scroll=%d status=%q", m.cursor, m.scroll, m.status)
	}
}

func TestReloadKeepsFocusedRow(t *testing.T) {
	fresh := snapshot( // axios (row 0) is gone
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/api", 4, "Bump eslint from 8.57.0 to 9.1.0"),
	)
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return fresh, nil }}
	for _, tt := range []struct {
		name string
		keys []string
		want string
	}{
		{"pr row", []string{"j"}, "acme/api#4"},
		{"group row", []string{"j", "j"}, "lodash"},
	} {
		m, cmd := press(newTest(fixture(), deps), append(tt.keys, "g")...)
		nm, _ := m.Update(cmd())
		m = nm.(Model)
		r := m.rows()[m.cursor]
		got := ""
		if r.isGroup() {
			got = r.group.Package
		} else {
			got = r.pr.Ref
		}
		if got != tt.want {
			t.Errorf("%s: focused %q after reload (cursor %d), want %q", tt.name, got, m.cursor, tt.want)
		}
	}
}

func TestReloadOnOtherScreenKeepsStatus(t *testing.T) {
	deps := Deps{Load: func(context.Context) (*model.Snapshot, error) { return fixture(), nil }}
	m, cmd := press(newTest(fixture(), deps), "g")
	m, _ = press(m, "d")
	want := m.status
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if m.status != want || m.reloading || m.popup != popupDetails {
		t.Errorf("status=%q want %q reloading=%v popup=%v", m.status, want, m.reloading, m.popup)
	}
}

func TestReloadKeepsQueuedPRsHidden(t *testing.T) {
	fq := newFakeQueue()
	deps := Deps{Queue: fq, Load: func(context.Context) (*model.Snapshot, error) { return fixture(), nil }}
	m, _ := press(selectLodash(newTest(fixture(), deps)), "y")
	m = deliver(m, fq.set(1, executor.JobDone, "", success("acme/api#1")))
	fq.ClearFinished()
	if got := listText(m); strings.Contains(got, "lodash") {
		t.Fatalf("a cleared job's PR stays hidden until the next reload:\n%s", got)
	}
	m, cmd := press(m, "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if got := listText(m); !strings.Contains(got, "acme/api#1  lodash") || strings.Contains(got, "acme/web#2") || len(m.rows()) != 3 {
		t.Errorf("after reload the cleared PR returns, the queued one stays hidden:\n%s", got)
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
