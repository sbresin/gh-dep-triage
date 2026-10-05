package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func newTestGH(t *testing.T, respond string, got *gqlRequest) *GH {
	t.Helper()
	opts := api.ClientOptions{Host: "github.com", AuthToken: "test", LogIgnoreEnv: true,
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, got); err != nil {
				t.Errorf("bad request body: %v", err)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(respond)), Request: r}, nil
		})}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return &GH{gql: gql}
}

func TestMutationsSendInputs(t *testing.T) {
	tests := []struct {
		name  string
		call  func(*GH) error
		field string
		vars  map[string]any
	}{
		{"approve", func(c *GH) error { return c.Approve(t.Context(), "PR_1", "abc") }, "addPullRequestReview(input: {pullRequestId: $id, commitOID: $oid, event: APPROVE})",
			map[string]any{"id": "PR_1", "oid": "abc"}},
		{"merge", func(c *GH) error { return c.Merge(t.Context(), "PR_1", "abc", "SQUASH") }, "mergePullRequest(input: {pullRequestId: $id, expectedHeadOid: $oid, mergeMethod: $method})",
			map[string]any{"id": "PR_1", "oid": "abc", "method": "SQUASH"}},
		{"automerge", func(c *GH) error { return c.EnableAutoMerge(t.Context(), "PR_1", "abc", "MERGE") }, "enablePullRequestAutoMerge(input: {pullRequestId: $id, expectedHeadOid: $oid, mergeMethod: $method})",
			map[string]any{"id": "PR_1", "oid": "abc", "method": "MERGE"}},
		{"comment", func(c *GH) error { return c.Comment(t.Context(), "PR_1", "@dependabot rebase") }, "addComment(input: {subjectId: $id, body: $body})",
			map[string]any{"id": "PR_1", "body": "@dependabot rebase"}},
		{"body", func(c *GH) error { return c.UpdateBody(t.Context(), "PR_1", "new") }, "updatePullRequest(input: {pullRequestId: $id, body: $body})",
			map[string]any{"id": "PR_1", "body": "new"}},
		{"close", func(c *GH) error { return c.Close(t.Context(), "PR_1") }, "closePullRequest(input: {pullRequestId: $id})",
			map[string]any{"id": "PR_1"}},
		{"request reviews", func(c *GH) error { return c.RequestReviews(t.Context(), "PR_1", nil, []string{"T_1"}) },
			"requestReviews(input: {pullRequestId: $id, userIds: $users, teamIds: $teams, union: true})", map[string]any{"id": "PR_1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got gqlRequest
			c := newTestGH(t, `{"data":{}}`, &got)
			if err := tt.call(c); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got.Query, "mutation(") || !strings.Contains(got.Query, tt.field) {
				t.Errorf("query = %s", got.Query)
			}
			for k, v := range tt.vars {
				if got.Variables[k] != v {
					t.Errorf("var %s = %v, want %v", k, got.Variables[k], v)
				}
			}
		})
	}
}

func TestMutationErrorIsReturned(t *testing.T) {
	var got gqlRequest
	c := newTestGH(t, `{"data":{"mergePullRequest":null},"errors":[{"message":"Head branch was modified. Review and try the merge again.","path":["mergePullRequest"]}]}`, &got)
	err := c.Merge(t.Context(), "PR_1", "abc", "SQUASH")
	if err == nil || !strings.Contains(err.Error(), "Head branch was modified") {
		t.Errorf("err = %v", err)
	}
}

func TestReviewerID(t *testing.T) {
	tests := []struct {
		name, reviewer, respond, query, id string
		team                               bool
		notFound                           bool
	}{
		{"user", "alice", `{"data":{"user":{"id":"U_1"}}}`, "user(login: $login)", "U_1", false, false},
		{"team", "acme/platform", `{"data":{"organization":{"team":{"id":"T_1"}}}}`, "team(slug: $slug)", "T_1", true, false},
		{"missing team", "acme/nope", `{"data":{"organization":{"team":null}}}`, "team(slug: $slug)", "", true, true},
		{"missing user", "ghost", `{"data":{"user":null},"errors":[{"type":"NOT_FOUND","path":["user"],"message":"Could not resolve to a User with the login of 'ghost'."}]}`,
			"user(login: $login)", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got gqlRequest
			id, team, err := newTestGH(t, tt.respond, &got).ReviewerID(t.Context(), tt.reviewer)
			if tt.notFound != errors.Is(err, ErrReviewerNotFound) || (!tt.notFound && err != nil) {
				t.Fatalf("err = %v, notFound want %v", err, tt.notFound)
			}
			if id != tt.id || team != tt.team || !strings.Contains(got.Query, tt.query) {
				t.Errorf("id=%q team=%v query=%s", id, team, got.Query)
			}
		})
	}
}

func TestRerunFailedJobsPostsToRun(t *testing.T) {
	var method, path string
	opts := api.ClientOptions{Host: "github.com", AuthToken: "test", LogIgnoreEnv: true,
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			method, path = r.Method, r.URL.Path
			return &http.Response{StatusCode: 201, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
		})}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&GH{rest: rest}).RerunFailedJobs(t.Context(), "acme/api", 42); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || !strings.HasSuffix(path, "/repos/acme/api/actions/runs/42/rerun-failed-jobs") {
		t.Errorf("%s %s", method, path)
	}
}
