package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/safe"
)

// shortRef is the repo name without its owner plus the number, e.g. "api#3".
func shortRef(pr *model.PR) string {
	name := pr.Repo
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return fmt.Sprintf("%s#%d", name, pr.Number)
}

// jobDetail is the step while running, "queued", or the outcome.
func jobDetail(j executor.Job) string {
	switch j.State {
	case executor.JobQueued:
		return "queued"
	case executor.JobRunning:
		if j.Step == "" {
			return "running…"
		}
		return j.Step + "…"
	}
	if j.Result.Status == model.ResultSuccess {
		return strings.Join(j.Result.Steps, ", ")
	}
	return resultDetail(j.Result)
}

func jobStyle(j executor.Job) lipgloss.Style {
	switch {
	case j.State == executor.JobRunning:
		return styleCyan
	case j.State == executor.JobQueued:
		return lipgloss.NewStyle()
	case j.State == executor.JobDone && j.Result.Status == model.ResultSuccess:
		return styleDim
	case j.State == executor.JobDone && (j.Result.Status == model.ResultFailed || j.Result.Status == model.ResultDenied):
		return styleRed
	default:
		return styleYellow
	}
}

// queueBody renders the jobs; under 100 columns only icon and short ref.
func (m Model) queueBody(width, height int) []string {
	if len(m.jobs) == 0 {
		return []string{styleDim.Render(clip("Nothing queued yet. Mark PRs and press c.", width))}
	}
	out := []string{}
	for i := m.qscroll; i < min(len(m.jobs), m.qscroll+height); i++ {
		j := m.jobs[i]
		text := jobIcon(j) + " " + shortRef(j.PR)
		if m.width >= 100 {
			text += "  " + updateLabel(j.PR) + "  " + jobDetail(j)
		}
		text = clip(text, width)
		style := jobStyle(j)
		if m.focus == paneQueue && i == m.qcursor {
			style = style.Reverse(true)
		}
		out = append(out, style.Render(text+strings.Repeat(" ", max(0, width-lipgloss.Width(text)))))
	}
	return out
}

func (m *Model) fixQueueScroll() {
	m.qcursor = max(0, min(m.qcursor, len(m.jobs)-1))
	m.qscroll = adjustScroll(m.qcursor, m.qscroll, m.bodyHeight(), len(m.jobs))
}

func (m Model) focusedJob() (executor.Job, bool) {
	if m.qcursor >= 0 && m.qcursor < len(m.jobs) {
		return m.jobs[m.qcursor], true
	}
	return executor.Job{}, false
}

func (m Model) updateQueue(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q":
		return m.requestQuit()
	case "esc":
		m.focus = paneList
		return m, nil
	}
	if nm, cmd, handled := m.prAction(k); handled {
		return nm, cmd
	}
	j, ok := m.focusedJob()
	switch k {
	case "j", "down":
		m.qcursor = min(m.qcursor+1, len(m.jobs)-1)
		m.qfollow = m.qcursor >= len(m.jobs)-1
	case "k", "up":
		m.qcursor = max(0, m.qcursor-1)
		m.qfollow = m.qcursor >= len(m.jobs)-1
	case "x":
		return m.cancelJob(j, ok)
	case "r":
		return m.retryJob(j, ok)
	case "C":
		if m.deps.Queue != nil {
			m.deps.Queue.ClearFinished()
			m.syncJobs(false)
		}
		m.status = "Cleared finished jobs."
	case "p":
		m.togglePause()
	}
	m.fixQueueScroll()
	return m, nil
}

func (m Model) cancelJob(j executor.Job, ok bool) (tea.Model, tea.Cmd) {
	switch {
	case !ok:
		m.status = "The queue is empty."
	case j.State != executor.JobQueued:
		m.status = "Only queued jobs can be cancelled."
	default:
		if err := m.deps.Queue.Cancel(j.ID); err != nil {
			m.status = fmt.Sprintf("Could not cancel %s: %v", j.PR.Ref, err)
		} else {
			m.status = "Cancelled " + j.PR.Ref + "."
		}
		m.syncJobs(false)
	}
	return m, nil
}

// retryJob re-confirms a finished, unsuccessful job with the PR as currently
// loaded, so it is pinned to the head the user sees now.
func (m Model) retryJob(j executor.Job, ok bool) (tea.Model, tea.Cmd) {
	switch {
	case !ok:
		m.status = "The queue is empty."
	case !j.Finished() || (j.State == executor.JobDone && j.Result.Status == model.ResultSuccess):
		m.status = "Only failed, skipped or cancelled jobs can be retried."
	default:
		pr := m.snap.Find(j.PR.PRRef())
		if pr == nil {
			m.status = j.PR.Ref + " is not in the current list; reload first (g)."
			return m, nil
		}
		return m.openConfirm([]*model.PR{pr}, &j), nil
	}
	return m, nil
}

func (m *Model) togglePause() {
	if m.deps.Queue == nil {
		return
	}
	m.paused = !m.paused
	if m.paused {
		m.deps.Queue.Pause()
		m.status = "Queue paused; running jobs finish."
		return
	}
	m.deps.Queue.Resume()
	m.status = "Queue resumed."
}

// jobText is the queue pane's b view: the job's outcome and the PR's blockers.
func jobText(j executor.Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s\n", j.PR.Ref, j.Action, jobWord(j))
	if j.State == executor.JobRunning && j.Step != "" {
		fmt.Fprintf(&b, "step: %s\n", j.Step)
	}
	if j.Finished() {
		r := j.Result
		fmt.Fprintf(&b, "status: %s\n", r.Status)
		if r.Reason != "" {
			fmt.Fprintf(&b, "reason: %s\n", r.Reason)
		}
		if r.Message != "" {
			fmt.Fprintf(&b, "message: %s\n", r.Message)
		}
		if len(r.Steps) > 0 {
			fmt.Fprintf(&b, "steps: %s\n", strings.Join(r.Steps, ", "))
		}
	}
	b.WriteString("\n")
	b.WriteString(blockerText(j.PR))
	return safe.Text(b.String())
}
