package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFull(t *testing.T) {
	p := write(t, `
policy:
  allowMajor: true
  repos:
    allow: ["acme/*"]
    deny: ["acme/legacy-*"]
bots: [dependabot, renovate, my-renovate]
defaults:
  team: platform
  limit: 50
`)
	got, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Policy:   Policy{AllowMajor: true, Repos: Repos{Allow: []string{"acme/*"}, Deny: []string{"acme/legacy-*"}}},
		Bots:     []string{"dependabot", "renovate", "my-renovate"},
		Defaults: Defaults{Team: "platform", Limit: 50},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestLoadMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yml")
	got, err := Load(missing, false)
	if err != nil || got.Bots != nil || got.Policy.AllowMajor {
		t.Errorf("optional missing file: %+v, %v", got, err)
	}
	if _, err := Load(missing, true); err == nil {
		t.Error("required missing file must fail")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	if _, err := Load(write(t, ""), true); err != nil {
		t.Errorf("empty file: %v", err)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := map[string]string{
		"unknown key":    "polcy:\n  allowMajor: true\n",
		"empty bots":     "bots: []\n",
		"bad glob":       "policy:\n  repos:\n    deny: [\"acme/[\"]\n",
		"negative limit": "defaults:\n  limit: -1\n",
		"bad yaml":       "policy: [\n",
	}
	for name, body := range tests {
		if _, err := Load(write(t, body), true); err == nil {
			t.Errorf("%s: want error", name)
		} else if !strings.Contains(err.Error(), "config") {
			t.Errorf("%s: error should mention config: %v", name, err)
		}
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := DefaultPath(); got != "/xdg/gh-dep-triage/config.yml" {
		t.Errorf("got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got := DefaultPath(); got != "/home/u/.config/gh-dep-triage/config.yml" {
		t.Errorf("got %q", got)
	}
}
