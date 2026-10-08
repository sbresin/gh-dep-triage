package tui

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// at moves the list cursor to the row of ref.
func at(t *testing.T, m Model, ref string) Model {
	t.Helper()
	for i, r := range m.rows() {
		if r.pr != nil && r.pr.Ref == ref {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no row for %s", ref)
	return m
}

func TestRebaseKeyQueuesCursorPR(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(at(t, newTest(fixture(), Deps{Queue: fq}), "acme/api#3"), "r")
	if m.popup != popupConfirm || !strings.Contains(plain(m), "Rebase acme/api#3?") {
		t.Fatalf("popup:\n%s", plain(m))
	}
	m, _ = press(m, "y")
	if diff := cmp.Diff([]string{"rebase acme/api#3 sha3"}, fq.submitted); diff != "" || m.status != "Queued 1 PR(s) for rebase." {
		t.Errorf("status=%q (-want +got):\n%s", m.status, diff)
	}
}

func TestUnblockKeysOnGroupRow(t *testing.T) {
	m, _ := press(newTest(fixture(), Deps{Queue: newFakeQueue()}), "j", "j")
	for _, k := range []string{"r", "R", "x"} {
		got, _ := press(m, k)
		if got.popup != popupNone || got.status != "Expand the group to act on one PR." {
			t.Errorf("%s: popup=%d status=%q", k, got.popup, got.status)
		}
	}
}

func TestRerunKey(t *testing.T) {
	m, _ := press(at(t, newTest(fixture(), Deps{Queue: newFakeQueue()}), "acme/api#3"), "R")
	if m.popup != popupNone || m.status != "No failing checks on acme/api#3." {
		t.Errorf("popup=%d status=%q", m.popup, m.status)
	}
	failing := snapshot(mkPR("acme/api", 7, "Bump axios from 1.6.0 to 1.7.0", func(p *model.PR) {
		p.CheckRuns = []model.Check{{Name: "test", Kind: model.CheckKindRun, Status: "COMPLETED", Conclusion: "FAILURE"}}
	}))
	fq := newFakeQueue()
	m, _ = press(at(t, newTest(failing, Deps{Queue: fq}), "acme/api#7"), "R")
	if !strings.Contains(plain(m), "Re-run failed checks on acme/api#7?") {
		t.Fatalf("popup:\n%s", plain(m))
	}
	press(m, "y")
	if diff := cmp.Diff([]string{"rerun acme/api#7 sha7"}, fq.submitted); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestCloseKey(t *testing.T) {
	m, _ := press(at(t, newTest(fixture(), Deps{Queue: newFakeQueue()}), "acme/api#3"), "x")
	if m.popup != popupNone || m.status != "acme/api#3 is not superseded." {
		t.Errorf("popup=%d status=%q", m.popup, m.status)
	}
	snap := snapshot(
		mkPR("acme/api", 5, "Bump axios from 1.6.0 to 1.7.0"),
		mkPR("acme/api", 6, "Bump axios from 1.6.0 to 1.7.1"),
	)
	fq := newFakeQueue()
	m, _ = press(at(t, newTest(snap, Deps{Queue: fq}), "acme/api#5"), "x")
	if !strings.Contains(plain(m), "Close acme/api#5 as superseded?") {
		t.Fatalf("popup:\n%s", plain(m))
	}
	press(m, "y")
	if diff := cmp.Diff([]string{"close acme/api#5 sha5 map[reason:superseded]"}, fq.submitted); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestRetryKeepsAction(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(at(t, newTest(fixture(), Deps{Queue: fq}), "acme/api#3"), "r", "y")
	m = deliver(m, fq.set(1, executor.JobDone, "", model.Result{Action: "rebase", Ref: "acme/api#3", Status: model.ResultFailed,
		Reason: model.ReasonMutationFailed, Steps: []string{}}))
	m, _ = press(m, "tab", "r")
	if m.popup != popupConfirm || !strings.Contains(plain(m), "Rebase acme/api#3?") {
		t.Fatalf("retry popup:\n%s", plain(m))
	}
}

// In the queue pane r and x still retry and cancel.
func TestQueuePaneKeysUnchanged(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(at(t, newTest(fixture(), Deps{Queue: fq}), "acme/api#3"), "r", "y")
	m, _ = press(m, "tab", "x")
	if len(fq.cancelled) != 1 || m.popup != popupNone {
		t.Errorf("x in queue pane must cancel: cancelled=%v popup=%d", fq.cancelled, m.popup)
	}
}

func TestBlockedKeyHelp(t *testing.T) {
	m := at(t, newTest(fixture(), Deps{}), "acme/api#3")
	if !strings.Contains(plain(m), blockedKeys) {
		t.Errorf("blocked PR shows unblock keys:\n%s", plain(m))
	}
	m = at(t, m, "acme/api#4")
	if !strings.Contains(plain(m), listKeys) {
		t.Errorf("non-blocked PR shows list keys:\n%s", plain(m))
	}
}

func TestQueueRowNamesUnblockAction(t *testing.T) {
	j := executor.Job{Action: "rebase", State: executor.JobQueued}
	if got := jobDetail(j); got != "rebase: queued" {
		t.Errorf("jobDetail = %q", got)
	}
	j.Action = model.ActionMerge
	if got := jobDetail(j); got != "queued" {
		t.Errorf("merge jobDetail = %q", got)
	}
}
