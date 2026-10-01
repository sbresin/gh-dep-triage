package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDefaultTeam(t *testing.T) {
	writeConfig(t, "defaults:\n  team: platform\n")
	f := sampleFake()
	if code, _, _ := runApp(t, testApp(f), "list", "--json"); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(strings.Join(f.Queries, "\n"), "team-review-requested:acme/platform") {
		t.Errorf("queries = %v", f.Queries)
	}
}

func TestFlagOverridesConfigTeam(t *testing.T) {
	writeConfig(t, "defaults:\n  team: platform\n")
	f := sampleFake()
	runApp(t, testApp(f), "list", "--team", "other/team", "--json")
	joined := strings.Join(f.Queries, "\n")
	if !strings.Contains(joined, "team-review-requested:other/team") || strings.Contains(joined, "platform") {
		t.Errorf("queries = %v", f.Queries)
	}
}

func TestConfigBotsFilterAuthors(t *testing.T) {
	writeConfig(t, "bots: [my-renovate]\n")
	_, out, _ := runApp(t, testApp(sampleFake()), "list", "--json")
	if c := decodeEnvelope(t, out).Data.Counts; c.Total != 0 {
		t.Errorf("counts = %+v, want no PRs from dependabot/renovate", c)
	}
}

func TestConfigErrors(t *testing.T) {
	bad := writeConfig(t, "polcy: {}\n")
	missing := filepath.Join(t.TempDir(), "nope.yml")
	for _, args := range [][]string{
		{"list", "--json"},
		{"list", "--json", "--config", missing},
	} {
		code, out, _ := runApp(t, testApp(sampleFake()), args...)
		e := decodeEnvelope(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_config" {
			t.Errorf("%v (bad=%s): code=%d errors=%+v", args, bad, code, e.Errors)
		}
	}
}
