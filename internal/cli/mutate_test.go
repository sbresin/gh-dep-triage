package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

type resultsEnvelope struct {
	Command string `json:"command"`
	DryRun  bool   `json:"dryRun"`
	Data    struct {
		Results []model.Result `json:"results"`
		Counts  resultCounts   `json:"counts"`
	} `json:"data"`
	Errors []model.Problem `json:"errors"`
}

func decodeResults(t *testing.T, out string) resultsEnvelope {
	t.Helper()
	var e resultsEnvelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	return e
}

func summary(rs []model.Result) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, strings.TrimSpace(r.Ref+" "+r.Status+" "+r.Reason))
	}
	return out
}

func TestMergeDryRunJSON(t *testing.T) {
	f := sampleFake()
	code, out, _ := runApp(t, testApp(f), "merge", "group:lodash@4.17.21", "acme/api#4", "acme/api#99", "--json")
	e := decodeResults(t, out)
	want := []string{
		"acme/api#1 planned",
		"acme/web#2 denied checks_failing",
		"acme/api#4 denied major_requires_allow_major",
		"acme/api#99 failed not_eligible",
	}
	if diff := cmp.Diff(want, summary(e.Data.Results)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if code != ExitPartial || !e.DryRun || len(f.Calls) != 0 {
		t.Errorf("code=%d dryRun=%v calls=%v", code, e.DryRun, f.Calls)
	}
	assertGolden(t, "merge_dryrun.json", out)
}

func TestMergeDryRunHuman(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "merge", "group:lodash@4.17.21", "acme/api#4", "acme/api#99")
	assertGolden(t, "merge_dryrun.txt", out)
}

func TestMergeDryRunCallsNoMutation(t *testing.T) {
	f := sampleFake()
	code, out, _ := runApp(t, testApp(f), "merge", "acme/api#1", "--json")
	e := decodeResults(t, out)
	if code != ExitOK || len(f.Calls) != 0 || e.Data.Results[0].Status != model.ResultPlanned {
		t.Errorf("code=%d calls=%v results=%+v", code, f.Calls, e.Data.Results)
	}
}

func TestMergeYes(t *testing.T) {
	f := sampleFake()
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }
	code, out, errOut := runApp(t, testApp(f), "merge", "group:lodash@4.17.21", "--yes", "--json")
	e := decodeResults(t, out)
	if diff := cmp.Diff([]string{"acme/api#1 success", "acme/web#2 denied checks_failing"}, summary(e.Data.Results)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"approve acme/api#1 sha1", "merge acme/api#1 sha1 SQUASH"}, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if code != ExitPartial || e.DryRun || !strings.Contains(errOut, "success merge acme/api#1: approved, merged (squash)") {
		t.Errorf("code=%d dryRun=%v stderr=%q", code, e.DryRun, errOut)
	}
}

func TestAllDeniedExitsThree(t *testing.T) {
	code, _, _ := runApp(t, testApp(sampleFake()), "merge", "acme/web#2", "--json")
	if code != ExitDenied {
		t.Errorf("code = %d, want 3", code)
	}
}

func TestApproveMajorWithAutoMergeNeedsFlag(t *testing.T) {
	f := sampleFake()
	if code, _, _ := runApp(t, testApp(f), "approve", "acme/api#4", "--yes", "--json"); code != ExitDenied {
		t.Errorf("without flag: code = %d, want 3", code)
	}
	code, out, _ := runApp(t, testApp(f), "approve", "acme/api#4", "--yes", "--allow-major", "--json")
	if got := summary(decodeResults(t, out).Data.Results); code != ExitOK || !cmp.Equal(got, []string{"acme/api#4 skipped already_approved"}) {
		t.Errorf("with flag: code=%d results=%v", code, got)
	}
}

func TestApproveAutoMergeWithFailingChecksDenied(t *testing.T) {
	f := sampleFake()
	f.PRs["acme/api#4"].CheckRuns = []model.Check{githubtest.CheckRun("test", "COMPLETED", "FAILURE", 102)}
	code, out, _ := runApp(t, testApp(f), "approve", "acme/api#4", "--allow-major", "--json")
	if got := summary(decodeResults(t, out).Data.Results); code != ExitDenied || !cmp.Equal(got, []string{"acme/api#4 denied checks_failing"}) {
		t.Errorf("code=%d results=%v", code, got)
	}
}

func TestConfigDenyRepo(t *testing.T) {
	writeConfig(t, "policy:\n  repos:\n    deny: [\"acme/*\"]\n")
	code, out, _ := runApp(t, testApp(sampleFake()), "merge", "acme/api#1", "--json")
	if got := summary(decodeResults(t, out).Data.Results); code != ExitDenied || !cmp.Equal(got, []string{"acme/api#1 denied repo_denied"}) {
		t.Errorf("code=%d results=%v", code, got)
	}
}

func TestMutateRequiresRefs(t *testing.T) {
	for _, cmd := range []string{"approve", "merge"} {
		code, out, _ := runApp(t, testApp(sampleFake()), cmd, "--json")
		e := decodeResults(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != "invalid_argument" || !e.DryRun {
			t.Errorf("%s: code=%d envelope=%+v", cmd, code, e)
		}
	}
}

func TestExitCodeFor(t *testing.T) {
	r := func(statuses ...string) []model.Result {
		out := []model.Result{}
		for _, s := range statuses {
			out = append(out, model.Result{Status: s})
		}
		return out
	}
	tests := []struct {
		rs     []model.Result
		dryRun bool
		want   int
	}{
		{r("planned", "denied"), true, ExitOK},
		{r("planned", "failed"), true, ExitPartial},
		{r("denied", "denied"), true, ExitDenied},
		{r("success", "skipped"), false, ExitOK},
		{r("success", "denied"), false, ExitPartial},
		{r("failed"), false, ExitPartial},
		{r("denied"), false, ExitDenied},
		{r(), false, ExitOK},
		{[]model.Result{{Status: "success"}, {Status: "skipped", Reason: "cancelled"}}, false, ExitPartial},
		{[]model.Result{{Status: "skipped", Reason: "cancelled"}}, false, ExitPartial},
		{[]model.Result{{Status: "skipped", Reason: "already_approved"}}, false, ExitOK},
	}
	for _, tt := range tests {
		if got := exitCodeFor(tt.rs, tt.dryRun); got != tt.want {
			t.Errorf("exitCodeFor(%v, %v) = %d, want %d", tt.rs, tt.dryRun, got, tt.want)
		}
	}
}
