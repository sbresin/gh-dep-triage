package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func newTest(snap *model.Snapshot, deps Deps) Model {
	if deps.Who == "" {
		deps.Who = "user octocat"
	}
	m, _ := New(context.Background(), snap, deps).Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	return m.(Model)
}

func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var nm tea.Model
		nm, cmd = m.Update(key(k))
		m = nm.(Model)
	}
	return m, cmd
}

func plain(m Model) string { return ansi.Strip(m.View().Content) }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestNavigationAndExpand(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "j", "j", "j", "j")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want clamped 2", m.cursor)
	}
	m, _ = press(m, "enter")
	if len(m.rows()) != 5 || !strings.Contains(plain(m), "└ 󰄱 acme/web#2") {
		t.Errorf("group should expand:\n%s", plain(m))
	}
	m, _ = press(m, "k", "up", "k", "k")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	m, _ = press(m, "enter")
	if !strings.Contains(m.status, "Enter expands or collapses grouped rows.") {
		t.Errorf("status = %q", m.status)
	}
}

func TestSpaceRefusesBlockedAndGroupSelectsReadyOnly(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "space")
	if len(m.selected) != 0 || !strings.Contains(m.status, "acme/api#3 is blocked") {
		t.Errorf("blocked: selected=%v status=%q", m.selected, m.status)
	}
	m, _ = press(m, "j", "space")
	if len(m.selected) != 0 || !strings.Contains(m.status, "acme/api#4 is merging") {
		t.Errorf("merging: selected=%v status=%q", m.selected, m.status)
	}
	m, _ = press(m, "j", "space")
	if !m.selected["acme/api#1"] || !m.selected["acme/web#2"] || m.status != "Selected 2 PRs in the group." {
		t.Errorf("group select: selected=%v status=%q", m.selected, m.status)
	}
	m, _ = press(m, "space")
	if len(m.selected) != 0 || m.status != "Cleared 2 PRs in the group." {
		t.Errorf("group clear: selected=%v status=%q", m.selected, m.status)
	}

	mixed := snapshot(
		mkPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		mkPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21", func(p *model.PR) { p.MergeStateStatus = "DIRTY" }),
	)
	m = newTest(mixed, Deps{})
	m, _ = press(m, "space")
	if diff := cmp.Diff(map[string]bool{"acme/api#1": true}, m.selected); diff != "" {
		t.Errorf("only ready PRs of a mixed group (-want +got):\n%s", diff)
	}
	m, _ = press(m, "enter", "j", "j", "space")
	if m.selected["acme/web#2"] {
		t.Error("blocked child row must not be selectable")
	}
}

func TestSortCycleFlattens(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "j", "s")
	if m.sortMode != "severity" || m.cursor != 0 || m.status != "Sorted by severity." || len(m.rows()) != 4 {
		t.Errorf("sort: mode=%s cursor=%d status=%q rows=%d", m.sortMode, m.cursor, m.status, len(m.rows()))
	}
}

func TestConfirmNeedsSelection(t *testing.T) {
	m, _ := press(newTest(fixture(), Deps{}), "c")
	if m.popup != popupNone || m.status != "Select at least one PR before confirming." {
		t.Errorf("popup=%d status=%q", m.popup, m.status)
	}
}

func TestQuitAndCtrlC(t *testing.T) {
	m, cmd := press(newTest(fixture(), Deps{}), "q")
	if !isQuit(cmd) || m.Interrupted() {
		t.Error("q quits without interrupt")
	}
	m, cmd = press(newTest(fixture(), Deps{}), "ctrl+c")
	if !isQuit(cmd) || !m.Interrupted() {
		t.Error("ctrl+c quits as interrupted")
	}
}

func TestTinyTerminal(t *testing.T) {
	m := newTest(fixture(), Deps{})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 5})
	m = nm.(Model)
	if got := plain(m); got != "Terminal too small for dep-triage." {
		t.Errorf("view = %q", got)
	}
	m, _ = press(m, "j", "space", "enter")
	if _, cmd := press(m, "q"); !isQuit(cmd) {
		t.Error("q quits on a tiny terminal")
	}
	nm, _ = m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	_ = plain(nm.(Model))
}

func TestScrollFollowsCursor(t *testing.T) {
	m := newTest(fixture(), Deps{})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	m = nm.(Model)
	m, _ = press(m, "j", "j", "enter", "j", "j")
	if m.cursor != 4 || m.scroll != 2 {
		t.Errorf("cursor=%d scroll=%d, want 4/2", m.cursor, m.scroll)
	}
	if lines := strings.Split(plain(m), "\n"); len(lines) != 8 {
		t.Errorf("view has %d lines, want 8", len(lines))
	}
}

func TestWarningsShownInStatus(t *testing.T) {
	s := fixture()
	s.Warnings = []model.Problem{{Code: "fetch_failed", Ref: "acme/x#1", Message: "HTTP 502"}}
	m := newTest(s, Deps{})
	if m.status != "1 warning(s) while loading; first: acme/x#1 HTTP 502" {
		t.Errorf("status = %q", m.status)
	}
}

func TestListViewGolden(t *testing.T) {
	m := newTest(fixture(), Deps{})
	m, _ = press(m, "j", "j", "enter", "j", "space")
	assertGolden(t, "list.txt", plain(m))
}
