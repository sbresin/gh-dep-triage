package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// queueModel has four jobs: api#1 done, web#2 failed (head_changed),
// api#3 running ("approving"), api#4 queued.
func queueModel(t *testing.T, width int) (Model, *fakeQueue) {
	t.Helper()
	fq := newFakeQueue()
	var opened []string
	m := newTest(fixture(), Deps{Queue: fq,
		Browse: func(url string) error { opened = append(opened, url); return nil },
		Load:   func(context.Context) (*model.Snapshot, error) { return fixture(), nil }})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
	m = nm.(Model)
	for _, ref := range []string{"acme/api#1", "acme/web#2", "acme/api#3", "acme/api#4"} {
		if _, err := fq.Submit(model.ActionMerge, m.findPR(ref)); err != nil {
			t.Fatal(err)
		}
	}
	fq.set(1, executor.JobDone, "", success("acme/api#1"))
	fq.set(2, executor.JobDone, "", model.Result{Action: "merge", Ref: "acme/web#2", Status: model.ResultFailed,
		Reason: model.ReasonHeadChanged, Message: "head moved from sha2 to sha9 since you confirmed", Steps: []string{}})
	return deliver(m, fq.set(3, executor.JobRunning, "approving", model.Result{})), fq
}

func TestSplitLayoutGolden(t *testing.T) {
	for _, tt := range []struct {
		width  int
		golden string
	}{{120, "split_wide.txt"}, {80, "split_narrow.txt"}} {
		m, _ := queueModel(t, tt.width)
		m.snap = snapshot(append(fixture().PRs(), mkPR("acme/ops", 9, "Bump zod from 3.0.0 to 3.0.1"))...) // one unqueued row
		m.fixScroll()
		got := plain(m)
		lines := strings.Split(got, "\n")
		if len(lines) != 12 {
			t.Errorf("%d: %d lines, want 12", tt.width, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > tt.width {
				t.Errorf("%d: line width %d: %q", tt.width, w, l)
			}
		}
		for _, want := range []string{"queue ✓1 ⟳1 ⏳1 ✗1", "Queue 4 · ✓1 ⟳1 ⏳1 ✗1", iconRunning + " api#3", "acme/ops#9  zod -> 3.0.1"} {
			if !strings.Contains(got, want) {
				t.Errorf("%d: missing %q:\n%s", tt.width, want, got)
			}
		}
		assertGolden(t, tt.golden, got)
	}
}

func TestKeyHelpFits100Columns(t *testing.T) {
	for _, k := range []string{listKeys, queueKeys} {
		if w := lipgloss.Width(k); w > 100 {
			t.Errorf("key help is %d columns: %q", w, k)
		}
	}
}

func TestTabSwitchesFocusAndKeys(t *testing.T) {
	m, _ := queueModel(t, 120)
	if m.focus != paneList || !strings.Contains(plain(m), listKeys) {
		t.Fatalf("starts on the list pane")
	}
	m, _ = press(m, "tab")
	if m.focus != paneQueue || !strings.Contains(plain(m), queueKeys) {
		t.Errorf("tab focuses the queue pane:\n%s", plain(m))
	}
	m, _ = press(m, "esc")
	if m.focus != paneList {
		t.Error("esc in the queue pane returns to the list")
	}
}

func TestQueueCursorFollowsNewJobsUntilMoved(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab")
	if m.qcursor != 3 {
		t.Fatalf("cursor follows the newest job: %d", m.qcursor)
	}
	m, _ = press(m, "k")
	_, _ = fq.Submit(model.ActionMerge, m.findPR("acme/web#2"))
	m = deliver(m, executor.Event{Kind: executor.EventQueued, Job: fq.jobs[4]})
	if m.qcursor != 2 {
		t.Errorf("a moved cursor stays put: %d", m.qcursor)
	}
	m, _ = press(m, "j", "j")
	_, _ = fq.Submit(model.ActionMerge, m.findPR("acme/api#1"))
	m = deliver(m, executor.Event{Kind: executor.EventQueued, Job: fq.jobs[5]})
	if m.qcursor != 5 {
		t.Errorf("back on the last job, the cursor follows again: %d", m.qcursor)
	}
}

func TestCancelKey(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab", "x")
	if diff := cmp.Diff([]executor.JobID{4}, fq.cancelled); diff != "" || m.status != "Cancelled acme/api#4." {
		t.Errorf("cancel queued: status=%q (-want +got):\n%s", m.status, diff)
	}
	m, _ = press(m, "k", "x")
	if m.status != "Only queued jobs can be cancelled." || len(fq.cancelled) != 1 {
		t.Errorf("cancel running: status=%q", m.status)
	}
}

func TestRetryKey(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab", "k", "k") // web#2, failed
	m, _ = press(m, "r")
	if m.popup != popupConfirm || !strings.Contains(plain(m), "Approve + Merge 1 PRs?") || strings.Contains(plain(m), "new commit") {
		t.Fatalf("retry opens the confirm popup:\n%s", plain(m))
	}
	m, _ = press(m, "y")
	if got := fq.submitted[len(fq.submitted)-1]; got != "merge acme/web#2 sha2" {
		t.Errorf("resubmitted %q", got)
	}

	m, fq = queueModel(t, 120)
	moved := *fq.jobs[1].PR
	moved.HeadOid = "sha-old"
	fq.jobs[1].PR = &moved
	m.syncJobs(false)
	m, _ = press(m, "tab", "k", "k", "r")
	if !strings.Contains(plain(m), "new commit since the last attempt") {
		t.Errorf("moved head is flagged:\n%s", plain(m))
	}

	m, _ = queueModel(t, 120)
	m, _ = press(m, "tab", "k", "k", "k", "r") // api#1, succeeded
	if m.popup != popupNone || m.status != "Only failed, skipped or cancelled jobs can be retried." {
		t.Errorf("retry success: popup=%d status=%q", m.popup, m.status)
	}

	m, fq = queueModel(t, 120)
	gone := *fq.jobs[1].PR
	gone.Repo, gone.Ref, gone.Number = "acme/gone", "acme/gone#7", 7
	fq.jobs[1].PR = &gone
	m.syncJobs(false)
	m, _ = press(m, "tab", "k", "k", "r")
	if m.popup != popupNone || !strings.Contains(m.status, "reload first (g)") {
		t.Errorf("retry of an unloaded PR: status=%q", m.status)
	}
}

func TestClearKeepsBadgesUntilReload(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab", "C")
	if fq.cleared != 1 || len(m.jobs) != 2 || m.status != "Cleared finished jobs." {
		t.Fatalf("cleared=%d jobs=%d status=%q", fq.cleared, len(m.jobs), m.status)
	}
	if _, ok := m.badges["acme/api#1"]; !ok {
		t.Error("the list badge of a cleared job stays until the next reload")
	}
	m, cmd := press(m, "tab", "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if _, ok := m.badges["acme/api#1"]; ok {
		t.Error("reload drops badges of cleared jobs")
	}
}

func TestPauseKey(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab", "p")
	if !fq.paused || !m.paused || !strings.Contains(plain(m), "| paused |") || !strings.Contains(plain(m), "· paused") {
		t.Errorf("pause: fq=%v m=%v\n%s", fq.paused, m.paused, plain(m))
	}
	m, _ = press(m, "p")
	if fq.paused || m.paused || m.status != "Queue resumed." {
		t.Errorf("resume: fq=%v m=%v status=%q", fq.paused, m.paused, m.status)
	}
}

func TestQueueDetailsAndPRActions(t *testing.T) {
	m, _ := queueModel(t, 120)
	m, _ = press(m, "tab", "k", "k") // web#2
	d, _ := press(m, "b")
	if got := plain(d); d.screen != screenPager || !strings.Contains(got, "Job: acme/web#2") ||
		!strings.Contains(got, "head_changed") || !strings.Contains(got, "since you confirmed") {
		t.Errorf("job details:\n%s", got)
	}
	d, _ = press(m, "d")
	if !strings.Contains(plain(d), "Description: acme/web#2") {
		t.Errorf("description of the job's PR:\n%s", plain(d))
	}
	_, cmd := press(m, "o")
	if msg, ok := cmd().(browsedMsg); !ok || msg.ref != "acme/web#2" {
		t.Errorf("o opens the job's PR: %#v", msg)
	}
}

func TestEmptyQueuePane(t *testing.T) {
	m, _ := press(newTest(fixture(), Deps{Queue: newFakeQueue()}), "tab")
	if !strings.Contains(plain(m), "Nothing queued yet") {
		t.Errorf("empty pane:\n%s", plain(m))
	}
	for _, k := range []string{"x", "r"} {
		if got, _ := press(m, k); got.status != "The queue is empty." {
			t.Errorf("%s: status=%q", k, got.status)
		}
	}
}

func TestQueuePaneSanitizes(t *testing.T) {
	m, fq := queueModel(t, 120)
	m = deliver(m, fq.set(4, executor.JobDone, "", model.Result{Ref: "acme/api#4", Status: model.ResultFailed,
		Reason: model.ReasonMutationFailed, Message: "\x1b]52;c;SGVsbG8=\x07evil\x9b31m", Steps: []string{}}))
	if raw := m.View().Content; strings.Contains(raw, "\x1b]52") || strings.Contains(raw, "\x07") || strings.Contains(raw, "\x9b") {
		t.Errorf("control sequence leaked: %q", raw)
	}
}

func TestClearKeepsCursorOnFocusedJob(t *testing.T) {
	m, fq := queueModel(t, 120)
	_, _ = fq.Submit(model.ActionMerge, m.findPR("acme/web#2"))
	m.syncJobs(false)
	m, _ = press(m, "tab", "k", "C", "x") // api#4, queued
	if diff := cmp.Diff([]executor.JobID{4}, fq.cancelled); diff != "" {
		t.Errorf("x after C cancels the focused job (-want +got):\n%s", diff)
	}
}

func TestNoPauseWhileQuittingWhenIdle(t *testing.T) {
	m, fq := queueModel(t, 120)
	m, _ = press(m, "tab", "q", "w", "p")
	if !m.quitWhenIdle || fq.paused || m.paused || m.status != "Can't pause while quitting when idle; press w or esc to stay." {
		t.Errorf("fq=%v m=%v status=%q", fq.paused, m.paused, m.status)
	}
}
