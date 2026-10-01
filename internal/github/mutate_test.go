package github

import (
	"encoding/json"
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
