package tui

import (
	"fmt"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// mkPR returns an enriched, ready, viewer-not-yet-approved Dependabot PR.
func mkPR(repo string, n int, title string, mods ...func(*model.PR)) *model.PR {
	p := &model.PR{
		ID: fmt.Sprintf("PR_%s#%d", repo, n), Repo: repo, Number: n, Ref: fmt.Sprintf("%s#%d", repo, n),
		Title: title, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, n), Author: "dependabot",
		State: "OPEN", CreatedAt: testNow.Add(-24 * time.Hour), HeadOid: fmt.Sprintf("sha%d", n), BaseRef: "main",
		MergeStateStatus: "BLOCKED", Mergeable: "MERGEABLE", ReviewDecision: "REVIEW_REQUIRED",
		RequestedForReview: true,
		RepoSettings:       model.RepoSettings{SquashMergeAllowed: true, AutoMergeAllowed: true, ViewerDefaultMergeMethod: "SQUASH"},
	}
	for _, m := range mods {
		m(p)
	}
	triage.Enrich(p)
	return p
}

func snapshot(prs ...*model.PR) *model.Snapshot {
	return &model.Snapshot{Viewer: "octocat", GeneratedAt: testNow, Groups: triage.Build(prs, testNow), Warnings: []model.Problem{}}
}

// Fixture used across tui tests:
//
//	acme/api#1, acme/web#2  lodash 4.17.20 -> 4.17.21 (group of two, ready)
//	acme/api#3              axios 1.6.0 -> 1.7.0, behind base (blocked)
//	acme/api#4              eslint 8.57.0 -> 9.1.0, auto-merge on (merging)
func fixture() *model.Snapshot {
	return snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0", func(p *model.PR) { p.MergeStateStatus = "BEHIND" }),
		mkPR("acme/api", 4, "Bump eslint from 8.57.0 to 9.1.0", func(p *model.PR) {
			p.AutoMerge = true
			p.ReviewDecision, p.MergeStateStatus = "APPROVED", "CLEAN"
			p.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow}}
		}),
	)
}

// fakeQueue is an in-memory tui.Queue. Tests move jobs along with set and
// feed the returned event to Update.
type fakeQueue struct {
	jobs      []executor.Job
	submitted []string
	submitErr map[string]error
	cancelled []executor.JobID
	paused    bool
	cleared   int
	closed    []bool
	events    chan executor.Event
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{submitErr: map[string]error{}, events: make(chan executor.Event)}
}

func (f *fakeQueue) Submit(action string, pr *model.PR) (executor.JobID, error) {
	if len(f.closed) > 0 {
		return 0, executor.ErrClosed
	}
	if err := f.submitErr[pr.Ref]; err != nil {
		return 0, err
	}
	f.submitted = append(f.submitted, action+" "+pr.Ref+" "+pr.HeadOid)
	id := executor.JobID(len(f.jobs) + 1)
	f.jobs = append(f.jobs, executor.Job{ID: id, Action: action, PR: pr, State: executor.JobQueued})
	return id, nil
}

func (f *fakeQueue) Cancel(id executor.JobID) error {
	for i := range f.jobs {
		if f.jobs[i].ID != id {
			continue
		}
		if f.jobs[i].State != executor.JobQueued {
			return executor.ErrNotCancellable
		}
		f.jobs[i].State = executor.JobCancelled
		f.jobs[i].Result = model.Result{Action: f.jobs[i].Action, Ref: f.jobs[i].PR.Ref, Status: model.ResultSkipped,
			Reason: model.ReasonCancelled, Message: "cancelled before it started", Steps: []string{}}
		f.cancelled = append(f.cancelled, id)
		return nil
	}
	return executor.ErrUnknownJob
}

func (f *fakeQueue) Pause()  { f.paused = true }
func (f *fakeQueue) Resume() { f.paused = false }

func (f *fakeQueue) ClearFinished() {
	f.cleared++
	kept := []executor.Job{}
	for _, j := range f.jobs {
		if !j.Finished() {
			kept = append(kept, j)
		}
	}
	f.jobs = kept
}

func (f *fakeQueue) Jobs() []executor.Job          { return append([]executor.Job(nil), f.jobs...) }
func (f *fakeQueue) Events() <-chan executor.Event { return f.events }
func (f *fakeQueue) Stop(cancelQueued bool)        { f.closed = append(f.closed, cancelQueued) }

// set moves job id to state and returns the event the real queue would send.
func (f *fakeQueue) set(id executor.JobID, state executor.JobState, step string, r model.Result) executor.Event {
	for i := range f.jobs {
		if f.jobs[i].ID != id {
			continue
		}
		f.jobs[i].State, f.jobs[i].Step, f.jobs[i].Result = state, step, r
		kind := executor.EventStep
		if f.jobs[i].Finished() {
			kind = executor.EventFinished
		}
		return executor.Event{Kind: kind, Job: f.jobs[i]}
	}
	panic(fmt.Sprintf("no job %d", id))
}

func deliver(m Model, ev executor.Event) Model {
	nm, _ := m.Update(queueMsg{ev: ev})
	return nm.(Model)
}

func success(ref string) model.Result {
	return model.Result{Action: model.ActionMerge, Ref: ref, Status: model.ResultSuccess, Steps: []string{"approved", "merged (squash)"}}
}
