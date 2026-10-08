package cli

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/github/githubtest"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

var testNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gh-dep-triage-cli")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeConfig writes body as the default config file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := filepath.Join(dir, "gh-dep-triage", "config.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func testApp(f *githubtest.Fake) *app {
	return &app{
		now:       func() time.Time { return testNow },
		newClient: func() (github.Client, error) { return f, nil },
		sleep:     func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
}

// sampleFake: #1 ready, #2 blocked (flaky + real failure), #3 blocked
// (behind), #4 merging (approved by viewer, auto-merge, pending check).
func sampleFake() *githubtest.Fake {
	f := githubtest.NewFake("octocat")

	ready := githubtest.NewPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21")
	ready.CheckRuns = []model.Check{githubtest.CheckRun("test", "COMPLETED", "SUCCESS", 101)}

	failing := githubtest.NewPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21")
	failing.Body = "<details>\n<summary>Release notes</summary>\n<p>Fixes prototype pollution.</p>\n</details>"
	failing.CheckRuns = []model.Check{
		githubtest.CheckRun("test", "COMPLETED", "FAILURE", 777),
		{Name: "ci/legacy", Kind: model.CheckKindStatus, Conclusion: "ERROR", URL: "https://ci.example.com/1"},
	}

	behind := githubtest.NewPR("acme/api", 3, "chore(deps): update dependency react to v19.0.0")
	behind.Author = "renovate"
	behind.MergeStateStatus = "BEHIND"

	merging := githubtest.NewPR("acme/api", 4, "Bump eslint from 8.57.0 to 9.1.0")
	merging.AutoMerge = true
	merging.ReviewDecision = "APPROVED"
	merging.ViewerReviews = []model.Review{{State: "APPROVED", SubmittedAt: testNow.Add(-time.Hour)}}
	merging.CheckRuns = []model.Check{githubtest.CheckRun("test", "IN_PROGRESS", "", 102)}

	f.Add(ready, true, false)
	f.Add(failing, true, false)
	f.Add(behind, true, false)
	f.Add(merging, false, true)
	f.Logs[777] = "step 1\nstep 2\nFAIL: TestX\n"
	f.Files["acme/web#2"] = []model.ChangedFile{
		{Path: "package.json", Status: "modified", Additions: 1, Deletions: 1},
		{Path: "package-lock.json", Status: "modified", Additions: 5, Deletions: 5},
	}
	return f
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}
