package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

type envelopeView struct {
	SchemaVersion int    `json:"schemaVersion"`
	Command       string `json:"command"`
	Viewer        string `json:"viewer"`
	DryRun        bool   `json:"dryRun"`
	Data          struct {
		Counts struct{ Ready, Merging, Blocked, Total int } `json:"counts"`
	} `json:"data"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func decodeEnvelope(t *testing.T, out string) envelopeView {
	t.Helper()
	var e envelopeView
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	return e
}

func TestListJSON(t *testing.T) {
	code, out, _ := runApp(t, testApp(sampleFake()), "list", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	e := decodeEnvelope(t, out)
	if e.SchemaVersion != 1 || e.Command != "list" || e.Viewer != "octocat" || e.DryRun {
		t.Errorf("envelope = %+v", e)
	}
	c := e.Data.Counts
	if c.Ready != 1 || c.Merging != 1 || c.Blocked != 2 || c.Total != 4 {
		t.Errorf("counts = %+v", c)
	}
	assertGolden(t, "list.json", out)
}

func TestListTable(t *testing.T) {
	code, out, _ := runApp(t, testApp(sampleFake()), "list")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	assertGolden(t, "list.txt", out)
}

func TestListScopeApproved(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "list", "--scope", "approved", "--json")
	c := decodeEnvelope(t, out).Data.Counts
	if c.Total != 1 || c.Merging != 1 {
		t.Errorf("counts = %+v", c)
	}
}

func TestListInvalidScope(t *testing.T) {
	code, out, _ := runApp(t, testApp(sampleFake()), "list", "--scope", "nope", "--json")
	e := decodeEnvelope(t, out)
	if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" {
		t.Errorf("code=%d envelope=%+v", code, e)
	}
}

func TestListEmptyUsesArrays(t *testing.T) {
	_, out, _ := runApp(t, testApp(githubtest.NewFake("octocat")), "list", "--json")
	for _, want := range []string{`"groups": []`, `"warnings": []`, `"errors": []`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

func TestUnknownCommandExitsOne(t *testing.T) {
	code, _, errOut := runApp(t, testApp(sampleFake()), "nope")
	if code != ExitError || !strings.Contains(errOut, "unknown command") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

func TestArgumentErrorsEmitJSONEnvelope(t *testing.T) {
	tests := [][]string{
		{"list", "--json", "--bogus"},
		{"list", "--json", "extra"},
		{"--json", "nope"},
		{"list", "--limit", "abc", "--json"},
	}
	for _, args := range tests {
		code, out, _ := runApp(t, testApp(sampleFake()), args...)
		e := decodeEnvelope(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" {
			t.Errorf("%v: code=%d envelope=%+v", args, code, e)
		}
	}
}

func TestArgumentErrorWithoutJSONUsesStderr(t *testing.T) {
	code, out, errOut := runApp(t, testApp(sampleFake()), "list", "--bogus")
	if code != ExitError || out != "" || !strings.HasPrefix(errOut, "error: ") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestNotAuthenticated(t *testing.T) {
	a := testApp(nil)
	a.newClient = func() (github.Client, error) { return nil, github.ErrNotAuthenticated }
	code, out, _ := runApp(t, a, "list", "--json")
	e := decodeEnvelope(t, out)
	if code != ExitError || e.Errors[0].Code != "not_authenticated" || !strings.Contains(e.Errors[0].Message, "gh auth login") {
		t.Errorf("code=%d envelope=%+v", code, e)
	}
}

func mergeDenied(t *testing.T, out string) map[string]string {
	t.Helper()
	var e struct {
		Data struct {
			Groups []struct {
				PRs []struct{ Ref, MergeDenied string }
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, g := range e.Data.Groups {
		for _, p := range g.PRs {
			got[p.Ref] = p.MergeDenied
		}
	}
	return got
}

func TestListMergeDenied(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "list", "--json")
	want := map[string]string{"acme/api#1": "", "acme/web#2": "checks_failing", "acme/api#3": "", "acme/api#4": "major_requires_allow_major"}
	if diff := cmp.Diff(want, mergeDenied(t, out)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestListMergeDeniedHonoursConfig(t *testing.T) {
	writeConfig(t, "policy:\n  allowMajor: true\n")
	_, out, _ := runApp(t, testApp(sampleFake()), "list", "--json")
	if got := mergeDenied(t, out)["acme/api#4"]; got != "" {
		t.Errorf("allowMajor in config: mergeDenied = %q, want empty", got)
	}
}

func listRefs(t *testing.T, out string) []string {
	t.Helper()
	var e struct {
		Data struct {
			Groups []struct{ PRs []struct{ Ref string } }
		}
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, g := range e.Data.Groups {
		for _, p := range g.PRs {
			refs = append(refs, p.Ref)
		}
	}
	return refs
}

func TestListFilters(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"--status", "ready"}, []string{"acme/api#1"}},
		{[]string{"--status", "blocked,merging"}, []string{"acme/api#4", "acme/web#2", "acme/api#3"}},
		{[]string{"--status", "blocked", "--status", "merging"}, []string{"acme/api#4", "acme/web#2", "acme/api#3"}},
		{[]string{"--bump", "major"}, []string{"acme/api#4"}},
		{[]string{"--status", "blocked", "--bump", "patch"}, []string{"acme/web#2"}},
		{[]string{"--scope", "approved", "--status", "ready"}, []string{}},
	}
	for _, tt := range tests {
		code, out, _ := runApp(t, testApp(sampleFake()), append([]string{"list", "--json"}, tt.args...)...)
		if got := listRefs(t, out); code != ExitOK || !cmp.Equal(got, tt.want) {
			t.Errorf("%v: code=%d refs=%v, want %v", tt.args, code, got, tt.want)
		}
	}
}

func TestListInvalidFilters(t *testing.T) {
	for _, args := range [][]string{{"--status", "open"}, {"--bump", "huge"}} {
		code, out, _ := runApp(t, testApp(sampleFake()), append([]string{"list", "--json"}, args...)...)
		e := decodeEnvelope(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" {
			t.Errorf("%v: code=%d errors=%+v", args, code, e.Errors)
		}
	}
}

func TestChecksLabel(t *testing.T) {
	for want, c := range map[string]model.CheckSummary{
		"--":    {},
		"OK":    {Total: 2, Passed: 1, Skipped: 1},
		"SKIP":  {Total: 1, Skipped: 1},
		"F1/P1": {Total: 2, Failed: 1, Pending: 1},
	} {
		if got := checksLabel(c); got != want {
			t.Errorf("checksLabel(%+v) = %q, want %q", c, got, want)
		}
	}
}
