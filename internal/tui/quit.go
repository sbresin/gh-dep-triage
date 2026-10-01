package tui

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/executor"
)

// signalMsg is SIGINT or SIGTERM; it counts as a ctrl+c press.
type signalMsg struct{}

// ctxDoneMsg reports that the parent context was cancelled. It cancels like
// ctrl+c but is not a press: a SIGINT both cancels the context and arrives
// as signalMsg, and must count once.
type ctxDoneMsg struct{}

func (m Model) pending() (queued, running int) {
	for _, j := range m.jobs {
		switch j.State {
		case executor.JobQueued:
			queued++
		case executor.JobRunning:
			running++
		}
	}
	return queued, running
}

// requestQuit quits at once when nothing is pending, otherwise asks how.
func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	if q, r := m.pending(); q+r == 0 {
		return m, tea.Quit
	}
	m.popup = popupQuit
	return m, nil
}

// interrupt handles ctrl+c and signals (press) and a cancelled context: with
// nothing pending it quits; otherwise the first one cancels queued jobs and
// quits once running ones finish, and a second press quits immediately.
func (m Model) interrupt(press bool) (tea.Model, tea.Cmd) {
	m.interrupted = true
	if press {
		m.presses++
	}
	q, r := m.pending()
	switch {
	case q+r == 0 && !m.cancelling:
		return m, tea.Quit
	case m.presses >= 2:
		m.forced = true
		return m, tea.Quit
	case m.cancelling:
		return m, nil
	}
	return m.startCancel()
}

// startCancel closes the queue with cancel; the model quits on the Closed
// event, which comes after every Finished event.
func (m Model) startCancel() (tea.Model, tea.Cmd) {
	m.cancelling, m.quitWhenIdle, m.popup = true, false, popupNone
	m.status = "Cancelling queued jobs; quitting once running ones finish. ctrl+c again quits immediately."
	q := m.deps.Queue
	return m, func() tea.Msg {
		q.Close(true)
		return nil
	}
}

func (m Model) updateQuit(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "w":
		m.popup, m.quitWhenIdle = popupNone, true
		if m.paused {
			m.deps.Queue.Resume()
			m.paused = false
		}
		m.status = "Quitting once the queue is idle. Press w or esc to stay."
		if q, r := m.pending(); q+r == 0 {
			return m, tea.Quit
		}
	case "c":
		m.presses = max(m.presses, 1) // a later ctrl+c is the second press
		return m.startCancel()
	case "esc", "n", "q":
		m.popup = popupNone
	}
	return m, nil
}

func (m Model) viewQuit() string {
	q, r := m.pending()
	w := popupWidth(m.width)
	return popupBox([]string{
		styleBold.Render("Quit dep-triage?"),
		"",
		fmt.Sprintf("%d queued, %d running", q, r),
		"",
		clip("w  wait, quit once the queue is idle", w),
		clip("c  cancel queued, quit once running ones finish", w),
		clip("esc  back", w),
	})
}

// forward turns each OS signal into a signalMsg and a cancelled ctx into one
// ctxDoneMsg until stop is called.
func forward(ctx context.Context, sigs <-chan os.Signal, send func(tea.Msg)) (stop func()) {
	done := make(chan struct{})
	go func() {
		ctxDone := ctx.Done()
		for {
			select {
			case <-sigs:
				send(signalMsg{})
			case <-ctxDone:
				ctxDone = nil
				send(ctxDoneMsg{})
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
