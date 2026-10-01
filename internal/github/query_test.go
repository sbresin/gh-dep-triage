package github

import (
	"strings"
	"testing"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestSearchQueries(t *testing.T) {
	bots := []string{"dependabot", "renovate"}
	req, rev := SearchQueries("", bots)
	base := "is:pr is:open draft:false archived:false"
	authors := "author:app/dependabot author:app/renovate"
	if want := base + " review-requested:@me " + authors; req != want {
		t.Errorf("requested = %q, want %q", req, want)
	}
	if want := base + " reviewed-by:@me " + authors; rev != want {
		t.Errorf("reviewed = %q, want %q", rev, want)
	}
	if req, _ := SearchQueries("platform", bots); !strings.Contains(req, "team-review-requested:acme/platform") {
		t.Errorf("team query = %q", req)
	}
	if req, _ := SearchQueries("other/team", bots); !strings.Contains(req, "team-review-requested:other/team") {
		t.Errorf("org/team query = %q", req)
	}
}

func TestBuildPRBatchQuery(t *testing.T) {
	q, vars := buildPRBatchQuery([]model.PRRef{{Repo: "acme/api", Number: 12}, {Repo: "acme/web", Number: 7}})
	for _, want := range []string{
		"query($viewer: String!, $o0: String!, $n0: String!, $o1: String!, $n1: String!)",
		"pr0: repository(owner: $o0, name: $n0)",
		"pr1: repository(owner: $o1, name: $n1)",
		"pullRequest(number: 12)",
		"isRequired(pullRequestNumber: 12)",
		"isRequired(pullRequestNumber: 7)",
		"reviews(author: $viewer, last: 20)",
		"viewerDefaultMergeMethod",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q", want)
		}
	}
	if vars["o0"] != "acme" || vars["n0"] != "api" || vars["o1"] != "acme" || vars["n1"] != "web" {
		t.Errorf("vars = %v", vars)
	}
}

func TestBuildPRBatchQueryHasMutationFields(t *testing.T) {
	q, _ := buildPRBatchQuery([]model.PRRef{{Repo: "acme/api", Number: 12}})
	for _, want := range []string{
		"number id title",
		"isMergeQueueEnabled",
		"statusCheckRollup { state contexts(first: 100) { pageInfo { hasNextPage } nodes {",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q", want)
		}
	}
}
