package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestShowJSONWithLogs(t *testing.T) {
	code, out, _ := runApp(t, testApp(sampleFake()), "show", "acme/web#2", "--logs", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	var e struct {
		Data showData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	d := e.Data
	if d.PR.Ref != "acme/web#2" || d.Checkout != "gh pr checkout 2 -R acme/web" || len(d.Files) != 2 {
		t.Errorf("data = %+v", d)
	}
	if d.ReleaseNotes != "<p>Fixes prototype pollution.</p>" {
		t.Errorf("release notes = %q", d.ReleaseNotes)
	}
	if len(d.Logs) != 2 || d.Logs[0].JobID != 777 || !strings.Contains(d.Logs[0].Log, "FAIL: TestX") ||
		d.Logs[1].Check != "ci/legacy" || d.Logs[1].Error == "" {
		t.Errorf("logs = %+v", d.Logs)
	}
	assertGolden(t, "show.json", out)
}

func TestShowHuman(t *testing.T) {
	code, out, _ := runApp(t, testApp(sampleFake()), "show", "acme/web#2")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	assertGolden(t, "show.txt", out)
}

func TestShowWithoutLogsHasEmptyArray(t *testing.T) {
	_, out, _ := runApp(t, testApp(sampleFake()), "show", "acme/api#1", "--json")
	if !strings.Contains(out, `"logs": []`) || !strings.Contains(out, `"files": []`) {
		t.Errorf("want empty arrays:\n%s", out)
	}
}

func TestShowErrors(t *testing.T) {
	tests := []struct {
		args []string
		code string
	}{
		{[]string{"show", "--json"}, "invalid_argument"},
		{[]string{"show", "group:lodash@4.17.21", "--json"}, "invalid_argument"},
		{[]string{"show", "nonsense", "--json"}, "invalid_ref"},
		{[]string{"show", "acme/api#99", "--json"}, "not_found"},
	}
	for _, tt := range tests {
		code, out, _ := runApp(t, testApp(sampleFake()), tt.args...)
		e := decodeEnvelope(t, out)
		if code != ExitError || len(e.Errors) != 1 || e.Errors[0].Code != tt.code {
			t.Errorf("%v: code=%d errors=%+v", tt.args, code, e.Errors)
		}
	}
}

func TestTailLines(t *testing.T) {
	var b strings.Builder
	for i := range 250 {
		b.WriteString("line ")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\n")
	}
	got := strings.Split(tailLines(b.String(), 200), "\n")
	if len(got) != 200 {
		t.Errorf("got %d lines", len(got))
	}
	if tailLines("a\nb\n", 200) != "a\nb" {
		t.Error("short log must be returned whole without trailing newline")
	}
}

func TestShowRiskIsSanitized(t *testing.T) {
	var buf bytes.Buffer
	pr := &model.PR{Ref: "acme/api#1", Risk: &model.Risk{SourceRepo: "github.com/x/\x1b[31mred", Findings: []string{"\x1b]0;pwn\x07"}}}
	writeShow(&buf, showData{PR: pr}, time.Now())
	if strings.ContainsRune(buf.String(), '\x1b') || !strings.Contains(buf.String(), "Risk:") {
		t.Errorf("risk line not sanitized or missing:\n%q", buf.String())
	}
}
