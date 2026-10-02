package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

// Queue is the part of *executor.Queue the TUI uses.
type Queue interface {
	Submit(action string, pr *model.PR) (executor.JobID, error)
	Cancel(id executor.JobID) error
	Pause()
	Resume()
	ClearFinished()
	Jobs() []executor.Job
	Events() <-chan executor.Event
	Stop(cancelQueued bool)
}

var _ Queue = (*executor.Queue)(nil)

// queueMsg carries one queue event; closed is set once the channel closed.
type queueMsg struct {
	ev     executor.Event
	closed bool
}

func listen(ch <-chan executor.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		return queueMsg{ev: ev, closed: !ok}
	}
}

func refKey(ref string) string { return strings.ToLower(ref) }

func (m Model) onQueue(msg queueMsg) (tea.Model, tea.Cmd) {
	if msg.closed || msg.ev.Kind == executor.EventClosed {
		m.syncJobs(false)
		if m.cancelling {
			return m, tea.Quit
		}
		if msg.closed {
			return m, nil
		}
	}
	if msg.ev.Kind == executor.EventFinished {
		m.history = append(m.history, msg.ev.Job)
	}
	m.syncJobs(false)
	if q, r := m.pending(); m.quitWhenIdle && q+r == 0 {
		return m, tea.Quit
	}
	return m, listen(m.deps.Queue.Events())
}

// syncJobs reloads the job list from the queue. Badges follow the latest job
// per PR; reset (after a reload) drops badges of jobs no longer in the queue.
func (m *Model) syncJobs(reset bool) {
	if m.deps.Queue == nil {
		return
	}
	m.jobs = m.deps.Queue.Jobs()
	if reset {
		m.badges = map[string]executor.Job{}
	}
	for _, j := range m.jobs {
		m.badges[refKey(j.PR.Ref)] = j
	}
	if m.qfollow {
		m.qcursor = len(m.jobs) - 1
	}
	m.fixQueueScroll()
}

func jobIcon(j executor.Job) string {
	switch j.State {
	case executor.JobQueued:
		return iconQueued
	case executor.JobRunning:
		return iconRunning
	case executor.JobCancelled:
		return iconSkipped
	}
	switch j.Result.Status {
	case model.ResultSuccess:
		return iconDone
	case model.ResultFailed, model.ResultDenied:
		return iconFailed
	default:
		return iconSkipped
	}
}

func jobWord(j executor.Job) string {
	switch j.State {
	case executor.JobQueued:
		return "queued"
	case executor.JobRunning:
		return "running"
	case executor.JobCancelled:
		return "cancelled"
	}
	if j.Result.Status == model.ResultSuccess {
		return "done"
	}
	return j.Result.Status
}

// resultDetail is "reason: steps: message", leaving out empty parts.
func resultDetail(r model.Result) string {
	parts := []string{}
	if r.Reason != "" {
		parts = append(parts, r.Reason)
	}
	if len(r.Steps) > 0 {
		parts = append(parts, strings.Join(r.Steps, ", "))
	}
	if r.Message != "" {
		parts = append(parts, r.Message)
	}
	return strings.Join(parts, ": ")
}

// resultText is the message, or the steps when there is none (as on the CLI).
func resultText(r model.Result) string {
	if r.Message != "" {
		return r.Message
	}
	return strings.Join(r.Steps, ", ")
}

func summaryLine(status, ref, detail string) string {
	if detail == "" {
		detail = "-"
	}
	return fmt.Sprintf("%s %s: %s", status, safe.Line(ref), safe.Line(detail))
}

// Summary is one line per finished job, for stderr after the TUI exits. It
// merges the Finished events seen with the queue's current jobs, so jobs
// whose events were not consumed before quitting are included.
func (m Model) Summary() []string {
	seen := map[executor.JobID]bool{}
	jobs := []executor.Job{}
	add := func(j executor.Job) {
		if !seen[j.ID] {
			seen[j.ID] = true
			jobs = append(jobs, j)
		}
	}
	for _, j := range m.history {
		add(j)
	}
	var live []executor.Job
	if m.deps.Queue != nil {
		live = m.deps.Queue.Jobs()
	}
	for _, j := range live {
		if j.Finished() {
			add(j)
		}
	}
	lines := []string{}
	for _, j := range jobs {
		lines = append(lines, summaryLine(j.Result.Status, j.PR.Ref, resultText(j.Result)))
	}
	if m.forced {
		for _, j := range live {
			switch j.State {
			case executor.JobRunning:
				lines = append(lines, summaryLine("running (abandoned)", j.PR.Ref, j.Step))
			case executor.JobQueued:
				lines = append(lines, summaryLine("cancelled", j.PR.Ref, "not started"))
			}
		}
	}
	return lines
}
