package tui

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// pendingModel has api#1 running ("approving") and web#2 queued.
func pendingModel(t *testing.T) (Model, *fakeQueue) {
	t.Helper()
	fq := newFakeQueue()
	m, _ := press(selectLodash(newTest(fixture(), Deps{Queue: fq})), "y")
	return deliver(m, fq.set(1, executor.JobRunning, "approving", model.Result{})), fq
}

func step(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

func TestQuitPopupOpensWhenPending(t *testing.T) {
	m, _ := pendingModel(t)
	m, cmd := press(m, "q")
	if isQuit(cmd) || m.popup != popupQuit {
		t.Fatalf("q with pending work opens the popup: popup=%d", m.popup)
	}
	got := plain(m)
	for _, want := range []string{"1 queued, 1 running", "w  wait", "c  cancel queued", "esc  back"} {
		if !strings.Contains(got, want) {
			t.Errorf("popup missing %q:\n%s", want, got)
		}
	}
	m, cmd = press(m, "esc")
	if isQuit(cmd) || m.popup != popupNone {
		t.Error("esc goes back")
	}
}

func TestQuitWaitQuitsWhenIdle(t *testing.T) {
	m, fq := pendingModel(t)
	m, cmd := press(m, "q", "w")
	if isQuit(cmd) || !m.quitWhenIdle || !strings.Contains(plain(m), "quitting when idle") {
		t.Fatalf("w waits: quitWhenIdle=%v\n%s", m.quitWhenIdle, plain(m))
	}
	m, cmd = step(m, queueMsg{ev: fq.set(1, executor.JobDone, "", success("acme/api#1"))})
	close(fq.events) // so isQuit can run the listen cmd without blocking
	if isQuit(cmd) {
		t.Fatal("web#2 is still queued")
	}
	m, cmd = step(m, queueMsg{ev: fq.set(2, executor.JobDone, "", success("acme/web#2"))})
	if !isQuit(cmd) || m.Interrupted() {
		t.Errorf("idle queue quits with 0: quit=%v interrupted=%v", isQuit(cmd), m.Interrupted())
	}
	if diff := cmp.Diff([]string{"success acme/api#1: approved, merged (squash)", "success acme/web#2: approved, merged (squash)"}, m.Summary()); diff != "" {
		t.Errorf("summary (-want +got):\n%s", diff)
	}
}

func TestQuitWaitResumesPausedQueue(t *testing.T) {
	m, fq := pendingModel(t)
	m, _ = press(m, "tab", "p", "q", "w")
	if fq.paused || m.paused || !m.quitWhenIdle {
		t.Errorf("w resumes a paused queue: fq=%v m=%v wait=%v", fq.paused, m.paused, m.quitWhenIdle)
	}
}

func TestQuitWaitUndo(t *testing.T) {
	m, fq := pendingModel(t)
	m, _ = press(m, "q", "w", "esc")
	if m.quitWhenIdle {
		t.Fatal("esc undoes quitting when idle")
	}
	m, _ = step(m, queueMsg{ev: fq.set(1, executor.JobDone, "", success("acme/api#1"))})
	close(fq.events) // so isQuit can run the listen cmd without blocking
	if _, cmd := step(m, queueMsg{ev: fq.set(2, executor.JobDone, "", success("acme/web#2"))}); isQuit(cmd) {
		t.Error("no longer quits when idle")
	}
}

func TestQuitCancel(t *testing.T) {
	m, fq := pendingModel(t)
	m, cmd := press(m, "q", "c")
	if isQuit(cmd) || !m.cancelling || !strings.Contains(plain(m), "cancelling…") {
		t.Fatalf("c starts cancelling:\n%s", plain(m))
	}
	// c already called Queue.Stop(true).
	if diff := cmp.Diff([]bool{true}, fq.closed); diff != "" {
		t.Errorf("Stop calls (-want +got):\n%s", diff)
	}
	m, cmd = step(m, queueMsg{ev: executor.Event{Kind: executor.EventClosed}})
	if !isQuit(cmd) || m.Interrupted() {
		t.Errorf("quits with 0 once the queue closed: quit=%v interrupted=%v", isQuit(cmd), m.Interrupted())
	}
}

func TestCtrlCWithPendingCancelsThenForces(t *testing.T) {
	m, fq := pendingModel(t)
	m, cmd := press(m, "ctrl+c")
	if isQuit(cmd) || !m.cancelling || !m.Interrupted() {
		t.Fatalf("first ctrl+c cancels: cancelling=%v interrupted=%v", m.cancelling, m.Interrupted())
	}
	// ctrl+c already called Queue.Stop(true).
	if len(fq.closed) != 1 || !fq.closed[0] {
		t.Errorf("Stop(true) expected, got %v", fq.closed)
	}
	m, cmd = press(m, "ctrl+c")
	if !isQuit(cmd) || !m.forced {
		t.Fatalf("second ctrl+c quits at once: forced=%v", m.forced)
	}
	want := []string{"running (abandoned) acme/api#1: approving", "cancelled acme/web#2: not started"}
	if diff := cmp.Diff(want, m.Summary()); diff != "" {
		t.Errorf("summary (-want +got):\n%s", diff)
	}
}

func TestCtrlCWithNothingPendingQuits(t *testing.T) {
	for _, msg := range []tea.Msg{key("ctrl+c"), signalMsg{}, ctxDoneMsg{}} {
		m, cmd := step(newTest(fixture(), Deps{Queue: newFakeQueue()}), msg)
		if !isQuit(cmd) || !m.Interrupted() {
			t.Errorf("%T: quit=%v interrupted=%v", msg, isQuit(cmd), m.Interrupted())
		}
	}
}

// One SIGINT cancels cmd.Context() (ctxDoneMsg) and reaches the TUI
// (signalMsg); together they count as one press, in either order.
func TestSignalAndCtxDoneCountOnce(t *testing.T) {
	for _, order := range [][]tea.Msg{{ctxDoneMsg{}, signalMsg{}}, {signalMsg{}, ctxDoneMsg{}}} {
		m, _ := pendingModel(t)
		var cmd tea.Cmd
		for _, msg := range order {
			m, cmd = step(m, msg)
			if isQuit(cmd) {
				t.Fatalf("%T after %v quit at once", msg, order)
			}
		}
		if !m.cancelling {
			t.Errorf("%v: cancelling expected", order)
		}
		if m, cmd = step(m, signalMsg{}); !isQuit(cmd) || !m.forced {
			t.Errorf("%v: a second SIGINT forces the quit", order)
		}
	}
}

func TestSubmitWhileCancellingShowsError(t *testing.T) {
	fq := newFakeQueue()
	ops := mkPR("acme/ops", 9, "Bump zod from 3.0.0 to 3.0.1")
	// rows: axios (api#3), eslint (api#4), lodash group, zod (ops#9)
	m := newTest(snapshot(append(fixture().PRs(), ops)...), Deps{Queue: fq})
	m, _ = press(m, "j", "j", "space", "c", "y")
	m = deliver(m, fq.set(1, executor.JobRunning, "approving", model.Result{}))
	m, _ = press(m, "ctrl+c") // Stop(true): the queue now refuses new jobs
	m, _ = press(m, "j")
	if r := m.rows()[m.cursor]; r.pr == nil || r.pr.Ref != "acme/ops#9" {
		t.Fatalf("cursor on %+v", r)
	}
	m, _ = press(m, "space", "c", "y")
	if !strings.Contains(m.status, "Not queued: acme/ops#9: the queue is closed.") {
		t.Errorf("status = %q", m.status)
	}
}

// ANSI-stripped goldens of both popups over the final split layout.
func TestPopupGoldens(t *testing.T) {
	m := selectLodash(newTest(fixture(), Deps{Queue: newFakeQueue()}))
	assertGolden(t, "confirm_popup.txt", plain(m))
	q, _ := pendingModel(t)
	q, _ = press(q, "q")
	assertGolden(t, "quit_popup.txt", plain(q))
}

func TestForwardSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	got := make(chan tea.Msg, 4)
	stop := forward(ctx, sigs, func(msg tea.Msg) { got <- msg })
	next := func() tea.Msg {
		select {
		case msg := <-got:
			return msg
		case <-time.After(5 * time.Second):
			t.Fatal("nothing forwarded")
			return nil
		}
	}
	sigs <- os.Interrupt
	if _, ok := next().(signalMsg); !ok {
		t.Error("SIGINT becomes signalMsg")
	}
	sigs <- syscall.SIGTERM
	if _, ok := next().(signalMsg); !ok {
		t.Error("SIGTERM becomes signalMsg")
	}
	cancel()
	if _, ok := next().(ctxDoneMsg); !ok {
		t.Error("a cancelled context becomes ctxDoneMsg")
	}
	stop()
	sigs <- os.Interrupt
	select {
	case msg := <-got:
		t.Errorf("forwarded after stop: %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}
