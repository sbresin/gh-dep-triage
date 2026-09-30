package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"keeps\nnewlines\tand tabs", "keeps\nnewlines\tand tabs"},
		{"\x1b]52;c;SGVsbG8=\x07after", "]52;c;SGVsbG8=after"},
		{"\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"over\rwrite", "overwrite"},
		{"nul\x00bs\x08del\x7f", "nulbsdel"},
		{"c1\u009b31m\u0085x", "c131mx"},
		{"unicode ✓ é", "unicode ✓ é"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShowHumanStripsControlSequences(t *testing.T) {
	const evil = "\x1b]52;c;SGVsbG8=\x07\x1b[31m"
	f := sampleFake()
	pr := f.PRs["acme/web#2"]
	pr.Title = "Bump lodash" + evil + " from 4.17.20 to 4.17.21"
	pr.Body = "<details>\n<summary>Release notes</summary>\n" + evil + "notes\n</details>"
	pr.CheckRuns[0].Name = "test" + evil
	f.Logs[777] = "step 1\n" + evil + "FAIL\n"
	f.Files["acme/web#2"] = []model.ChangedFile{{Path: "pkg" + evil + ".json", Status: "modified"}}

	code, out, errOut := runApp(t, testApp(f), "show", "acme/web#2", "--logs")
	if code != ExitOK {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	if strings.ContainsAny(out+errOut, "\x1b\x07") {
		t.Errorf("control bytes reached the terminal:\n%q", out)
	}
	if !strings.Contains(out, "notes") || !strings.Contains(out, "FAIL") {
		t.Errorf("content lost:\n%s", out)
	}

	_, out, errOut = runApp(t, testApp(f), "list")
	if strings.ContainsAny(out+errOut, "\x1b\x07") {
		t.Errorf("control bytes reached the terminal in list:\n%q", out)
	}
}

func TestEmitSanitizesWarningsAndErrors(t *testing.T) {
	f := sampleFake()
	f.FailBatch["acme/api#1"] = errors.New("HTTP 502 \x1b[2J\x1b]0;pwned\x07")
	code, _, errOut := runApp(t, testApp(f), "show", "acme/api#1")
	if code != ExitError || !strings.Contains(errOut, "warning: acme/api#1: HTTP 502") || strings.ContainsAny(errOut, "\x1b\x07") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}
