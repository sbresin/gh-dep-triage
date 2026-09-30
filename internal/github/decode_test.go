package github

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func loadFixture(t *testing.T) map[string]*rawRepo {
	t.Helper()
	b, err := os.ReadFile("testdata/pr_batch.json")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]*rawRepo{}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodePRBatch(t *testing.T) {
	refs := []model.PRRef{
		{Repo: "acme/api", Number: 12}, {Repo: "acme/gone", Number: 1},
		{Repo: "acme/web", Number: 7}, {Repo: "acme/web", Number: 9},
	}
	prs, warnings := decodePRBatch(loadFixture(t), refs, map[string]string{"pr3": "Resource not accessible by integration"})

	wantWarnings := []model.Problem{
		{Code: "not_found", Ref: "acme/gone#1", Message: "pull request not found or not accessible"},
		{Code: "fetch_failed", Ref: "acme/web#9", Message: "Resource not accessible by integration"},
	}
	if diff := cmp.Diff(wantWarnings, warnings); diff != "" {
		t.Errorf("warnings (-want +got):\n%s", diff)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs", len(prs))
	}

	a := prs[0]
	if a.Ref != "acme/api#12" || a.Author != "dependabot" || a.HeadOid != "abc123" || a.AutoMerge ||
		a.MergeStateStatus != "BLOCKED" || a.ReviewDecision != "REVIEW_REQUIRED" || a.State != "OPEN" {
		t.Errorf("pr0 fields: %+v", a)
	}
	if diff := cmp.Diff([]string{"acme/platform", "octocat"}, a.RequestedReviewers); diff != "" {
		t.Errorf("reviewers (-want +got):\n%s", diff)
	}
	wantReviews := []model.Review{{State: "APPROVED", SubmittedAt: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}}
	if diff := cmp.Diff(wantReviews, a.ViewerReviews); diff != "" {
		t.Errorf("reviews (-want +got):\n%s", diff)
	}
	wantChecks := []model.Check{
		{Name: "test", Kind: model.CheckKindRun, Status: "COMPLETED", Conclusion: "FAILURE", Required: true,
			URL: "https://github.com/acme/api/actions/runs/555/job/777", JobID: 777, WorkflowRunID: 555},
		{Name: "ci/legacy", Kind: model.CheckKindStatus, Conclusion: "PENDING", URL: "https://ci.example.com/1"},
	}
	if diff := cmp.Diff(wantChecks, a.CheckRuns); diff != "" {
		t.Errorf("checks (-want +got):\n%s", diff)
	}
	wantSettings := model.RepoSettings{SquashMergeAllowed: true, AutoMergeAllowed: true, ViewerDefaultMergeMethod: "SQUASH"}
	if a.RepoSettings != wantSettings {
		t.Errorf("settings = %+v", a.RepoSettings)
	}

	b := prs[1]
	if !b.IsDraft || !b.AutoMerge || b.ReviewDecision != "" || b.CheckRuns == nil || len(b.CheckRuns) != 0 {
		t.Errorf("pr2 fields: %+v", b)
	}
}

func TestSearchHits(t *testing.T) {
	var r rawSearch
	raw := `{"search":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[
	  {"number":1,"title":"Bump a from 1 to 2","url":"u1","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"Bot","login":"dependabot"}},
	  {"number":2,"title":"t","url":"u2","repository":{"nameWithOwner":"acme/api"},"author":null},
	  {}
	]}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	want := []SearchHit{
		{Repo: "acme/api", Number: 1, Title: "Bump a from 1 to 2", URL: "u1", AuthorLogin: "dependabot", AuthorType: "Bot"},
		{Repo: "acme/api", Number: 2, Title: "t", URL: "u2"},
	}
	if diff := cmp.Diff(want, r.hits()); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
