package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sbresin/gh-dep-triage/skill"
)

func TestSkillPrintsSkillMarkdown(t *testing.T) {
	code, out, _ := runApp(t, testApp(nil), "skill")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out != skill.Content || !strings.HasPrefix(out, "---\nname: gh-dep-triage\n") {
		t.Errorf("stdout is not the embedded SKILL.md:\n%.200s", out)
	}
}

func TestSkillInstallWritesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gh-dep-triage", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runApp(t, testApp(nil), "skill", "install", "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != skill.Content {
		t.Fatalf("installed file = %.80q, %v; want embedded SKILL.md", got, err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("stdout = %q, want the installed path", out)
	}
}

func TestSkillInstallJSON(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := runApp(t, testApp(nil), "skill", "install", "--dir", dir, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env struct {
		Command string `json:"command"`
		Data    struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if want := filepath.Join(dir, "gh-dep-triage", "SKILL.md"); env.Command != "skill install" || env.Data.Path != want {
		t.Errorf("envelope = %+v, want command %q, path %q", env, "skill install", want)
	}
}

func TestSkillInstallDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code, _, errOut := runApp(t, testApp(nil), "skill", "install"); code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "gh-dep-triage", "SKILL.md")); err != nil {
		t.Error(err)
	}
}

// Every `gh dep-triage <cmd>` the skill tells an agent to run must exist.
func TestSkillOnlyMentionsRealCommands(t *testing.T) {
	root := (&app{}).rootCmd()
	seen := 0
	for _, m := range regexp.MustCompile(`gh dep-triage ([a-z-]+)`).FindAllStringSubmatch(skill.Content, -1) {
		seen++
		if c, _, err := root.Find([]string{m[1]}); err != nil || c == root {
			t.Errorf("SKILL.md mentions unknown command %q", m[1])
		}
	}
	if seen == 0 {
		t.Error("SKILL.md mentions no gh dep-triage commands")
	}
}
