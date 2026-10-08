package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

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

func TestPRActionsOnGroupRow(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "j", "j")
	for k, want := range map[string]string{
		"o": "Open works on PR rows only.",
		"d": "Details work on PR rows only.",
	} {
		got, _ := press(m, k)
		if got.popup != popupNone || got.status != want {
			t.Errorf("%s: popup=%d status=%q", k, got.popup, got.status)
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

// risky is the fixture with deps.dev data and a long description on the
// blocked acme/api#3, which the list cursor starts on.
func risky() *model.Snapshot {
	s := fixture()
	for _, p := range s.PRs() {
		if p.Ref == "acme/api#3" {
			p.Risk = &model.Risk{System: "NPM", SourceRepo: "github.com/axios/axios", Stars: 108000, Scorecard: 6.9,
				PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
			p.Body = "Bumps **axios** from 1.6.0 to 1.7.0.\n\n" + strings.Repeat("Release note line.\n\n", 30)
		}
	}
	return s
}

func detailsText(m Model) string { return ansi.Strip(m.details.content) }

func TestDetailsPopupFromList(t *testing.T) {
	m, _ := press(newTest(risky(), Deps{}), "d")
	if m.popup != popupDetails {
		t.Fatalf("popup = %d, want details", m.popup)
	}
	got := detailsText(m)
	for _, want := range []string{"Blockers", "Behind base branch", "behind_base", "Branch is behind main", "r  rebase",
		"Dependency (deps.dev)", "github.com/axios/axios", "108,000 ★", "scorecard 6.9", "published 2026-10-01",
		"Description", "Release note line."} {
		if !strings.Contains(got, want) {
			t.Errorf("details missing %q:\n%s", want, got)
		}
	}
	view := plain(m)
	for _, want := range []string{"dep-triage |", "acme/api#3  axios 1.6.0 -> 1.7.0 [minor]", "blocked", "r rebase", "esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "jump") {
		t.Errorf("no section jumping any more:\n%s", view)
	}
	m, _ = press(m, "esc")
	if m.popup != popupNone {
		t.Errorf("esc closes the popup, popup = %d", m.popup)
	}
}

func TestDetailsOpenAtTopAndScroll(t *testing.T) {
	m, _ := press(newTest(risky(), Deps{}), "d")
	if m.popup != popupDetails || m.details.vp.YOffset() != 0 {
		t.Fatalf("d opens the popup at the top: popup %d offset %d", m.popup, m.details.vp.YOffset())
	}
	m, _ = press(m, "j")
	if m.details.vp.YOffset() != 1 {
		t.Errorf("j scrolls: offset %d", m.details.vp.YOffset())
	}
	if b, _ := press(newTest(risky(), Deps{}), "b"); b.popup != popupNone {
		t.Errorf("b no longer opens details: popup %d", b.popup)
	}
}

func TestDetailsActionKeys(t *testing.T) {
	m, _ := press(newTest(risky(), Deps{}), "d", "r")
	if m.popup != popupConfirm || m.confirm.action != model.ActionRebase || m.confirm.prs[0].Ref != "acme/api#3" {
		t.Errorf("r in details opens the rebase confirm: popup %d action %q", m.popup, m.confirm.action)
	}
	m, _ = press(newTest(risky(), Deps{}), "d", "x")
	if m.popup != popupNone || m.status != "acme/api#3 is not superseded." {
		t.Errorf("x on a PR that isn't superseded: popup %d status %q", m.popup, m.status)
	}
}

func TestDetailsRiskSection(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) {
		p.Risk = &model.Risk{SourceRepo: "github.com/lodash/lodash", Stars: 61277, Scorecard: 7.5,
			PublishedAt: time.Date(2021, 2, 20, 15, 42, 16, 0, time.UTC), Deprecated: true,
			Advisories: []string{"GHSA-1"}, Findings: []string{"COOLDOWN", model.FindingMalicious},
			CooldownEnd: time.Date(2026, 10, 6, 19, 31, 20, 0, time.UTC)}
	})
	content := detailsContent(pr, nil, true, 80)
	got := ansi.Strip(content)
	for _, want := range []string{"61,277 ★", "scorecard 7.5", "published 2021-02-20", "deprecated",
		"COOLDOWN until 2026-10-06 19:31 UTC", "MALICIOUS", "advisories: GHSA-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	none := detailsContent(mkPR("acme/api", 2, "Bump a from 1.0.0 to 1.0.1"), nil, true, 80)
	if !strings.Contains(ansi.Strip(none), "No deps.dev data") {
		t.Error("PRs without risk data say so")
	}
}

func TestDetailsAreSanitized(t *testing.T) {
	pr := mkPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1", func(p *model.PR) {
		p.Body = "hi \x1b]52;c;SGVsbG8=\x07 there \x9b31m"
		p.Risk = &model.Risk{SourceRepo: "github.com/x/\x1b]52;c;SGVsbG8=\x07y", Findings: []string{"\x9b31mEVIL"}}
	})
	content := detailsContent(pr, nil, true, 80)
	if strings.Contains(content, "\x1b]52") || strings.Contains(content, "\x9b") {
		t.Errorf("control sequence leaked: %q", content)
	}
}

func TestDetailsFollowResize(t *testing.T) {
	m, _ := press(newTest(risky(), Deps{}), "d")
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = nm.(Model)
	if m.details.vp.Width() != m.detailsWidth() || m.details.vp.Height() != m.detailsHeight() {
		t.Errorf("viewport %dx%d, want %dx%d", m.details.vp.Width(), m.details.vp.Height(), m.detailsWidth(), m.detailsHeight())
	}
	for _, l := range strings.Split(plain(m), "\n") {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("line wider than the terminal: %q", l)
		}
	}
}

func TestDetailsGolden(t *testing.T) {
	m := newTest(risky(), Deps{})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = press(nm.(Model), "d")
	assertGolden(t, "details_popup.txt", plain(m))
}
