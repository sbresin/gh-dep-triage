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
	f := sampleFake()
	a := testApp(f)
	a.stdin = strings.NewReader(`[{"action":"merge","ref":"acme/api#1","headOid":"stale"},{"action":"approve","ref":"acme/api#3","headOid":"sha3"}]`)
	code, out, _ := runApp(t, a, "apply", "--plan", "-", "--json")
	e := decodeResults(t, out)
	got := summary(e.Data.Results)
	if diff := cmp.Diff([]string{"acme/api#1 failed head_changed", "acme/api#3 planned"}, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if code != ExitPartial {
		t.Errorf("code = %d, want 2", code)
	}
	if !e.DryRun || len(f.Calls) != 0 {
		t.Errorf("dryRun=%v calls=%v, want dry run with no calls", e.DryRun, f.Calls)
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
		msg   string
	}{
		"missing --plan":     {[]string{"apply", "--json"}, `[{"action":"approve","ref":"acme/api#3"}]`, "is required"},
		"positional args":    {[]string{"apply", "acme/api#1", "--plan", "-", "--json"}, "[]", "takes no arguments"},
		"bad json":           {[]string{"apply", "--plan", "-", "--json"}, "nope", "JSON array"},
		"unsupported action": {[]string{"apply", "--plan", "-", "--json"}, `[{"action":"rebase","ref":"acme/api#1"}]`, "rebase"},
		"missing file":       {[]string{"apply", "--plan", "/nonexistent/plan.json", "--json"}, "", "no such file"},
	}
	for name, tt := range tests {
		a := testApp(sampleFake())
		a.stdin = strings.NewReader(tt.stdin)
		code, out, _ := runApp(t, a, tt.args...)
		e := decodeResults(t, out)
		if code != ExitError || e.Command != "apply" || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" ||
			!strings.Contains(e.Errors[0].Message, tt.msg) {
			t.Errorf("%s: code=%d command=%q errors=%+v, want message containing %q", name, code, e.Command, e.Errors, tt.msg)
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

	a = testApp(sampleFake())
	a.stdin = strings.NewReader(`[{"action":"merge","ref":"acme/api#4"}]`)
	_, out, _ = runApp(t, a, "apply", "--plan", "-", "--json")
	if got := summary(decodeResults(t, out).Data.Results); !cmp.Equal(got, []string{"acme/api#4 denied major_requires_allow_major"}) {
		t.Errorf("without --allow-major: results = %v", got)
	}
}
