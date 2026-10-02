package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
)

// selectLodash marks the two-PR lodash group (third row) and opens the confirm popup.
func selectLodash(m Model) Model {
	m, _ = press(m, "j", "j", "space", "c")
	return m
}

func TestConfirmPopup(t *testing.T) {
	m := selectLodash(newTest(fixture(), Deps{Queue: newFakeQueue()}))
	if m.popup != popupConfirm {
		t.Fatalf("popup = %d", m.popup)
	}
	got := plain(m)
	for _, want := range []string{"Approve + Merge 2 PRs?", "acme/api  #1  lodash -> 4.17.21 [patch]",
		"acme/web  #2  lodash -> 4.17.21 [patch]", pinnedNote, "y queue  n/esc back"} {
		if !strings.Contains(got, want) {
			t.Errorf("popup missing %q:\n%s", want, got)
		}
	}
	if lines := strings.Split(got, "\n"); len(lines) != 12 {
		t.Errorf("view has %d lines, want 12", len(lines))
	}
	m, _ = press(m, "esc")
	if m.popup != popupNone || m.selectedCount() != 2 || m.status != "Nothing queued." {
		t.Errorf("esc keeps marks: popup=%d marked=%d status=%q", m.popup, m.selectedCount(), m.status)
	}
}

func TestConfirmShowsHardRuleDenial(t *testing.T) {
	fq := newFakeQueue()
	m := selectLodash(newTest(fixture(), Deps{Queue: fq, Rules: policy.Rules{Bots: []string{"renovate"}}}))
	if !strings.Contains(plain(m), "#1  lodash -> 4.17.21 [patch]  denied (not_bot_pr)") {
		t.Errorf("denial not shown:\n%s", plain(m))
	}
	m, _ = press(m, "y")
	if m.popup != popupNone || len(fq.submitted) != 0 || m.status != "Nothing to queue: every PR fails a hard rule." {
		t.Errorf("popup=%d submitted=%v status=%q", m.popup, fq.submitted, m.status)
	}
}

func TestConfirmQueuesMarkedPRs(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(selectLodash(newTest(fixture(), Deps{Queue: fq})), "y")
	if diff := cmp.Diff([]string{"merge acme/api#1 sha1", "merge acme/web#2 sha2"}, fq.submitted); diff != "" {
		t.Errorf("submitted (-want +got):\n%s", diff)
	}
	if len(m.selected) != 0 || m.status != "Queued 2 PR(s) for Approve+Merge." || len(m.jobs) != 2 {
		t.Errorf("selected=%v status=%q jobs=%d", m.selected, m.status, len(m.jobs))
	}
	if got := listText(m); strings.Contains(got, "lodash") || len(m.rows()) != 2 {
		t.Errorf("queued lodash rows leave the list (rows=%d):\n%s", len(m.rows()), got)
	}
}

func TestSubmitErrorIsShown(t *testing.T) {
	fq := newFakeQueue()
	fq.submitErr["acme/web#2"] = executor.ErrAlreadyQueued
	m, _ := press(selectLodash(newTest(fixture(), Deps{Queue: fq})), "y")
	if want := "Queued 1 PR(s) for Approve+Merge. Not queued: acme/web#2: already queued."; m.status != want {
		t.Errorf("status = %q, want %q", m.status, want)
	}
}

// listText is the list pane's body without styling.
func listText(m Model) string {
	return ansi.Strip(strings.Join(m.listBody(m.listWidth()-2, m.bodyHeight()), "\n"))
}

func TestQueuedPRsLeaveTheList(t *testing.T) {
	fq := newFakeQueue()
	m, _ := press(selectLodash(newTest(fixture(), Deps{Queue: fq,
		Load: func(context.Context) (*model.Snapshot, error) { return fixture(), nil }})), "y")
	hidden := func(when string) {
		t.Helper()
		got := listText(m)
		if strings.Contains(got, "acme/api#1") || strings.Contains(got, "acme/web#2") || strings.Contains(got, "lodash") || len(m.rows()) != 2 {
			t.Errorf("%s: queued PRs are hidden (rows=%d):\n%s", when, len(m.rows()), got)
		}
	}
	hidden("queued")
	m = deliver(m, fq.set(1, executor.JobDone, "", success("acme/api#1")))
	m = deliver(m, fq.set(2, executor.JobDone, "", model.Result{Ref: "acme/web#2", Status: model.ResultFailed, Steps: []string{}}))
	hidden("finished")
	m, _ = press(m, "space", "j", "space", "j", "space")
	if len(m.selected) != 0 {
		t.Errorf("only blocked rows are left to mark: selected=%v", m.selected)
	}
	m, _ = press(m, "tab", "C", "tab")
	hidden("cleared, before reload")
	m, cmd := press(m, "g")
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if got := listText(m); !strings.Contains(got, "lodash -> 4.17.21 [patch] (2 PRs)") || len(m.rows()) != 3 {
		t.Errorf("cleared and reloaded PRs return (rows=%d):\n%s", len(m.rows()), got)
	}
}

func TestAllPRsQueuedMessage(t *testing.T) {
	m, _ := press(newTest(snapshot(mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21")), Deps{Queue: newFakeQueue()}), "space", "c", "y")
	if got := listText(m); got != "All PRs are in the queue." {
		t.Errorf("list pane = %q", got)
	}
	if got := listText(newTest(snapshot(), Deps{})); got != "" {
		t.Errorf("an empty snapshot has no message: %q", got)
	}
}

func TestPopupScrollsAndWarnsMajors(t *testing.T) {
	var prs []*model.PR
	for n := 1; n <= 5; n++ {
		prs = append(prs, mkPR(fmt.Sprintf("acme/r%d", n), n, "Bump react from 18.0.0 to 19.0.0"))
	}
	m, _ := press(newTest(snapshot(prs...), Deps{Queue: newFakeQueue()}), "space", "c")
	got := plain(m)
	for _, want := range []string{"Approve + Merge 5 PRs?", "⚠ acme/r1  #1  react -> 19.0.0 [major]", "1-2 of 5, j/k scroll"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	m, _ = press(m, "j", "j", "j", "j", "j")
	if got := plain(m); !strings.Contains(got, "4-5 of 5") || !strings.Contains(got, "acme/r5  #5") {
		t.Errorf("scrolled popup:\n%s", got)
	}
}

func TestConfirmPopupOnSmallTerminal(t *testing.T) {
	m := selectLodash(newTest(fixture(), Deps{Queue: newFakeQueue()}))
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 8}, {Width: 41, Height: 6}} {
		nm, _ := m.Update(size)
		lines := strings.Split(plain(nm.(Model)), "\n")
		if len(lines) != size.Height {
			t.Errorf("%dx%d: %d lines", size.Width, size.Height, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > size.Width {
				t.Errorf("%dx%d: line width %d: %q", size.Width, size.Height, w, l)
			}
		}
	}
}

func TestOverlayNeverGrows(t *testing.T) {
	frame := strings.TrimSuffix(strings.Repeat(strings.Repeat(".", 40)+"\n", 8), "\n")
	popup := popupBox([]string{strings.Repeat("x", 70), "a", "b", "c", "d", "e", "f", "g", "h", "i"})
	lines := strings.Split(overlay(frame, popup, 40, 8), "\n")
	if len(lines) != 8 {
		t.Errorf("%d lines, want 8", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("width %d > 40: %q", w, l)
		}
	}
}
