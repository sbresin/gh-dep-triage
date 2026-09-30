package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
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

func TestNotAuthenticated(t *testing.T) {
	a := testApp(nil)
	a.newClient = func() (github.Client, error) { return nil, github.ErrNotAuthenticated }
	code, out, _ := runApp(t, a, "list", "--json")
	e := decodeEnvelope(t, out)
	if code != ExitError || e.Errors[0].Code != "not_authenticated" || !strings.Contains(e.Errors[0].Message, "gh auth login") {
		t.Errorf("code=%d envelope=%+v", code, e)
	}
}
