package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestDescriptionPager(t *testing.T) {
	s := snapshot(mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) {
		p.Body = "Bumps **lodash**.\n\nRelease notes here."
	}))
	tall, _ := newTest(s, Deps{}).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m, _ := press(tall.(Model), "d")
	if m.screen != screenPager {
		t.Fatalf("screen = %d", m.screen)
	}
	got := plain(m)
	for _, want := range []string{"Description: acme/api#1", "Bump lodash from 4.17.20 to 4.17.21", "Release notes here."} {
		if !strings.Contains(got, want) {
			t.Errorf("pager missing %q:\n%s", want, got)
		}
	}
	m, _ = press(m, "esc")
	if m.screen != screenList {
		t.Errorf("esc returns to list, screen = %d", m.screen)
	}
}

func TestDescriptionIsSanitized(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.Body = "hi \x1b]52;c;SGVsbG8=\x07 there \x9b31m"
	})
	out := describe(pr, 80)
	// Glamour emits its own SGR (and possibly OSC 8 link) sequences; what must
	// never appear is anything from the body: the OSC 52 opener or a C1 CSI.
	if strings.Contains(out, "\x1b]52") || strings.Contains(out, "\x9b") {
		t.Errorf("control sequence leaked: %q", out)
	}
	if !strings.Contains(describe(mkPR("acme/api", 2, "Bump b from 1.0.0 to 1.0.1"), 80), "No description provided") {
		t.Error("empty body placeholder missing")
	}
}

func TestBlockerPager(t *testing.T) {
	m, _ := press(newTest(fixture(), Deps{}), "b")
	got := plain(m)
	for _, want := range []string{"Blockers: acme/api#3", "behind_base", "gh dep-triage rebase acme/api#3 --yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("blocker pager missing %q:\n%s", want, got)
		}
	}
	m, _ = press(m, "q", "j", "b")
	if !strings.Contains(plain(m), "Auto-merge is enabled") {
		t.Errorf("merging PR explanation missing:\n%s", plain(m))
	}
}

func TestPRActionsOnGroupRow(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "j", "j")
	for k, want := range map[string]string{
		"o": "Open works on PR rows only.",
		"d": "Description view works on PR rows only.",
		"b": "Blocker details work on PR rows only.",
	} {
		got, _ := press(m, k)
		if got.screen != screenList || got.status != want {
			t.Errorf("%s: screen=%d status=%q", k, got.screen, got.status)
		}
	}
}

func TestOpenInBrowser(t *testing.T) {
	var opened []string
	deps := Deps{Browse: func(url string) error { opened = append(opened, url); return nil }}
	m, cmd := press(newTest(fixture(), deps), "o")
	if cmd == nil {
		t.Fatal("o must return a command")
	}
	nm, _ := m.Update(cmd())
	m = nm.(Model)
	if len(opened) != 1 || opened[0] != "https://github.com/acme/api/pull/3" || m.status != "Opened acme/api#3 in the browser." {
		t.Errorf("opened=%v status=%q", opened, m.status)
	}
	deps.Browse = func(string) error { return errors.New("no browser") }
	m, cmd = press(newTest(fixture(), deps), "o")
	nm, _ = m.Update(cmd())
	if got := nm.(Model).status; got != "Browser open failed for acme/api#3: no browser" {
		t.Errorf("status = %q", got)
	}
}

// A browser launched by `o` may write to the terminal behind Bubble Tea's back
// (e.g. Flatpak warnings); the TUI must repaint the whole screen afterwards.
func TestBrowsedRepaintsScreen(t *testing.T) {
	m := newTest(fixture(), Deps{})
	for _, msg := range []browsedMsg{{ref: "acme/api#3"}, {ref: "acme/api#3", err: errors.New("no browser")}} {
		_, cmd := m.Update(msg)
		if cmd == nil || !reflect.DeepEqual(cmd(), tea.ClearScreen()) {
			t.Errorf("browsedMsg %+v must return tea.ClearScreen", msg)
		}
	}
}

func TestBlockerPagerShowsRisk(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) {
		p.Risk = &model.Risk{System: "NPM", SourceRepo: "github.com/lodash/\x1b[31mlodash", Stars: 61277, Scorecard: 7.5,
			PublishedAt: time.Date(2021, 2, 20, 15, 42, 16, 0, time.UTC), Deprecated: true,
			Advisories: []string{"GHSA-1"}, Findings: []string{"COOLDOWN"}}
	})
	got := blockerText(pr)
	for _, want := range []string{"Dependency (deps.dev)", "github.com/lodash/", "61277 stars", "scorecard 7.5",
		"published 2021-02-20", "deprecated", "findings: COOLDOWN", "advisories: GHSA-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("escape leaked: %q", got)
	}
	if !strings.Contains(blockerText(mkPR("acme/api", 2, "Bump a from 1.0.0 to 1.0.1")), "No deps.dev data") {
		t.Error("PRs without risk data say so")
	}
}
