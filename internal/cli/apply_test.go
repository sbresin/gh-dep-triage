package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestApplyFromStdinChecksHead(t *testing.T) {
	a := testApp(sampleFake())
	a.stdin = strings.NewReader(`[{"action":"merge","ref":"acme/api#1","headOid":"stale"},{"action":"approve","ref":"acme/api#3","headOid":"sha3"}]`)
	code, out, _ := runApp(t, a, "apply", "--plan", "-", "--json")
	got := summary(decodeResults(t, out).Data.Results)
	if diff := cmp.Diff([]string{"acme/api#1 failed head_changed", "acme/api#3 planned"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if code != ExitPartial {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestApplyFromFileExecutes(t *testing.T) {
	f := sampleFake()
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, []byte(`[{"action":"approve","ref":"acme/api#3"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runApp(t, testApp(f), "apply", "--plan", p, "--yes", "--json")
	got := summary(decodeResults(t, out).Data.Results)
	if code != ExitOK || !cmp.Equal(got, []string{"acme/api#3 success"}) || !cmp.Equal(f.Calls, []string{"approve acme/api#3 sha3"}) {
		t.Errorf("code=%d results=%v calls=%v", code, got, f.Calls)
	}
}

func TestApplyErrors(t *testing.T) {
	tests := map[string]struct {
		args  []string
		stdin string
	}{
		"missing --plan":     {[]string{"apply", "--json"}, ""},
		"positional args":    {[]string{"apply", "acme/api#1", "--plan", "-", "--json"}, "[]"},
		"bad json":           {[]string{"apply", "--plan", "-", "--json"}, "nope"},
		"unsupported action": {[]string{"apply", "--plan", "-", "--json"}, `[{"action":"rebase","ref":"acme/api#1"}]`},
		"missing file":       {[]string{"apply", "--plan", "/nonexistent/plan.json", "--json"}, ""},
	}
	for name, tt := range tests {
		a := testApp(sampleFake())
		a.stdin = strings.NewReader(tt.stdin)
		code, out, _ := runApp(t, a, tt.args...)
		e := decodeResults(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" {
			t.Errorf("%s: code=%d errors=%+v", name, code, e.Errors)
		}
	}
}

func TestApplyHonoursAllowMajor(t *testing.T) {
	a := testApp(sampleFake())
	a.stdin = strings.NewReader(`[{"action":"merge","ref":"acme/api#4"}]`)
	_, out, _ := runApp(t, a, "apply", "--plan", "-", "--allow-major", "--json")
	got := decodeResults(t, out).Data.Results
	if len(got) != 1 || got[0].Status != model.ResultPlanned {
		t.Errorf("results = %+v", got)
	}
}
