package tui

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestQueueEventsUpdatePaneAndSummary(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(selectLodash(newTest(fixture(), Deps{Queue: fq})), "y")
	nm, cmd := m.Update(queueMsg{ev: fq.set(1, executor.JobDone, "", success("acme/api#1"))})
	m = nm.(Model)
	if cmd == nil {
		t.Error("the model keeps listening for events")
	}
	got := plain(m)
	if !strings.Contains(got, iconDone+" api#1") || !strings.Contains(got, iconQueued+" web#2") {
		t.Errorf("queue pane:\n%s", got)
	}
	if diff := cmp.Diff([]string{"success acme/api#1: approved, merged (squash)"}, m.Summary()); diff != "" {
		t.Errorf("summary (-want +got):\n%s", diff)
	}
	if _, cmd := m.Update(queueMsg{closed: true}); cmd != nil {
		t.Error("a closed event channel stops listening")
	}
}

func TestSummarySanitizes(t *testing.T) {
	m := newTest(fixture(), Deps{})
	pr := m.findPR("acme/api#1")
	m.history = []executor.Job{{ID: 1, PR: pr, State: executor.JobDone, Result: model.Result{
		Ref: pr.Ref, Status: model.ResultFailed, Reason: model.ReasonMutationFailed, Message: "\x1b[31mboom\nbad", Steps: []string{}}}}
	if diff := cmp.Diff([]string{"failed acme/api#1: [31mboom bad"}, m.Summary()); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestResultDetail(t *testing.T) {
	r := model.Result{Reason: model.ReasonMutationFailed, Steps: []string{"approved"}, Message: "boom"}
	if got := resultDetail(r); got != "mutation_failed: approved: boom" {
		t.Errorf("resultDetail = %q", got)
	}
	if got := resultDetail(model.Result{Steps: []string{}}); got != "" {
		t.Errorf("empty resultDetail = %q", got)
	}
}
