package github

import (
	"fmt"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

const BatchSize = 25

const searchBase = "is:pr is:open draft:false archived:false"

// SearchQueries returns the review-requested and reviewed-by search strings.
func SearchQueries(team string, bots []string) (requested, reviewed string) {
	authors := make([]string, len(bots))
	for i, b := range bots {
		authors[i] = "author:app/" + b
	}
	a := strings.Join(authors, " ")
	req := "review-requested:@me"
	if team != "" {
		if !strings.Contains(team, "/") {
			team = "acme/" + team
		}
		req = "team-review-requested:" + team
	}
	return searchBase + " " + req + " " + a, searchBase + " reviewed-by:@me " + a
}

const searchQuery = `query($q: String!, $first: Int!, $after: String) {
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { number title url repository { nameWithOwner } author { __typename login } } }
  }
}`

const repoFields = `nameWithOwner mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed autoMergeAllowed viewerDefaultMergeMethod`

func prFields(number int) string {
	return fmt.Sprintf(`pullRequest(number: %[1]d) {
      number title url body state isDraft createdAt updatedAt
      headRefOid baseRefName mergeStateStatus mergeable reviewDecision
      author { __typename login }
      autoMergeRequest { enabledAt }
      reviews(author: $viewer, last: 20) { nodes { state submittedAt } }
      reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { combinedSlug } ... on Bot { login } } } }
      commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
        __typename
        ... on CheckRun { name status conclusion detailsUrl databaseId isRequired(pullRequestNumber: %[1]d) checkSuite { workflowRun { databaseId } } }
        ... on StatusContext { context state targetUrl isRequired(pullRequestNumber: %[1]d) }
      } } } } } }
    }`, number)
}

// buildPRBatchQuery aliases each ref as pr<i>. The caller sets vars["viewer"].
func buildPRBatchQuery(refs []model.PRRef) (string, map[string]any) {
	params := []string{"$viewer: String!"}
	vars := map[string]any{}
	var body strings.Builder
	for i, r := range refs {
		owner, name, _ := strings.Cut(r.Repo, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		vars[fmt.Sprintf("o%d", i)] = owner
		vars[fmt.Sprintf("n%d", i)] = name
		fmt.Fprintf(&body, "  pr%d: repository(owner: $o%d, name: $n%d) { %s\n    %s\n  }\n", i, i, i, repoFields, prFields(r.Number))
	}
	return "query(" + strings.Join(params, ", ") + ") {\n" + body.String() + "}", vars
}
