package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

const maxLogBytes = 64 << 20

// GH is the go-gh backed Client.
type GH struct {
	gql  *api.GraphQLClient
	rest *api.RESTClient
}

var _ Client = (*GH)(nil)

func New() (*GH, error) {
	host, _ := auth.DefaultHost()
	if token, _ := auth.TokenForHost(host); token == "" {
		return nil, ErrNotAuthenticated
	}
	opts := api.ClientOptions{Host: host, Transport: newRetryTransport(http.DefaultTransport)}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, err
	}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	return &GH{gql: gql, rest: rest}, nil
}

func (c *GH) Viewer(ctx context.Context) (string, error) {
	var resp struct{ Viewer struct{ Login string } }
	if err := c.gql.DoWithContext(ctx, `query { viewer { login } }`, nil, &resp); err != nil {
		return "", mapAuthError(err)
	}
	return resp.Viewer.Login, nil
}

func (c *GH) SearchPRs(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	return searchAll(ctx, limit, func(ctx context.Context, first int, after string) (rawSearch, error) {
		vars := map[string]any{"q": query, "first": first}
		if after != "" {
			vars["after"] = after
		}
		var res rawSearch
		err := c.gql.DoWithContext(ctx, searchQuery, vars, &res)
		return res, err
	})
}

func (c *GH) FetchPRs(ctx context.Context, viewer string, refs []model.PRRef) ([]*model.PR, []model.Problem, error) {
	query, vars := buildPRBatchQuery(refs)
	vars["viewer"] = viewer
	data := map[string]*rawRepo{}
	errByAlias := map[string]string{}
	if err := c.gql.DoWithContext(ctx, query, vars, &data); err != nil {
		var gqlErr *api.GraphQLError
		if !errors.As(err, &gqlErr) {
			return nil, nil, err
		}
		// go-gh decodes data before reporting errors, so partial results survive.
		for _, item := range gqlErr.Errors {
			alias := ""
			if len(item.Path) > 0 {
				alias, _ = item.Path[0].(string)
			}
			if alias == "" {
				return nil, nil, err
			}
			errByAlias[alias] = item.Message
		}
	}
	prs, warnings := decodePRBatch(data, refs, errByAlias)
	return prs, warnings, nil
}

func (c *GH) PRFiles(ctx context.Context, ref model.PRRef) ([]model.ChangedFile, error) {
	var raw []struct {
		Filename  string
		Status    string
		Additions int
		Deletions int
	}
	path := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", ref.Repo, ref.Number)
	if err := c.rest.DoWithContext(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	files := make([]model.ChangedFile, 0, len(raw))
	for _, f := range raw {
		files = append(files, model.ChangedFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
	}
	return files, nil
}

func (c *GH) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	resp, err := c.rest.RequestWithContext(ctx, http.MethodGet, fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxLogBytes))
	return string(b), err
}

// mapAuthError turns an HTTP 401 (expired or revoked token) into
// ErrNotAuthenticated, keeping the original error in the chain.
func mapAuthError(err error) error {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %w", ErrNotAuthenticated, err)
	}
	return err
}

const (
	approveMutation   = `mutation($id: ID!, $oid: GitObjectID!) { addPullRequestReview(input: {pullRequestId: $id, commitOID: $oid, event: APPROVE}) { pullRequestReview { id } } }`
	mergeMutation     = `mutation($id: ID!, $oid: GitObjectID!, $method: PullRequestMergeMethod!) { mergePullRequest(input: {pullRequestId: $id, expectedHeadOid: $oid, mergeMethod: $method}) { pullRequest { merged } } }`
	autoMergeMutation = `mutation($id: ID!, $oid: GitObjectID!, $method: PullRequestMergeMethod!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, expectedHeadOid: $oid, mergeMethod: $method}) { pullRequest { autoMergeRequest { enabledAt } } } }`
	commentMutation   = `mutation($id: ID!, $body: String!) { addComment(input: {subjectId: $id, body: $body}) { clientMutationId } }`
	bodyMutation      = `mutation($id: ID!, $body: String!) { updatePullRequest(input: {pullRequestId: $id, body: $body}) { pullRequest { id } } }`
	reviewsMutation   = `mutation($id: ID!, $users: [ID!], $teams: [ID!]) { requestReviews(input: {pullRequestId: $id, userIds: $users, teamIds: $teams, union: true}) { pullRequest { id } } }`
	closeMutation     = `mutation($id: ID!) { closePullRequest(input: {pullRequestId: $id}) { pullRequest { state } } }`
	userIDQuery       = `query($login: String!) { user(login: $login) { id } }`
	teamIDQuery       = `query($org: String!, $slug: String!) { organization(login: $org) { team(slug: $slug) { id } } }`
)

func (c *GH) mutate(ctx context.Context, query string, vars map[string]any) error {
	var resp map[string]any
	return c.gql.DoWithContext(ctx, query, vars, &resp)
}

func (c *GH) Approve(ctx context.Context, prID, headOid string) error {
	return c.mutate(ctx, approveMutation, map[string]any{"id": prID, "oid": headOid})
}

func (c *GH) Merge(ctx context.Context, prID, headOid, method string) error {
	return c.mutate(ctx, mergeMutation, map[string]any{"id": prID, "oid": headOid, "method": method})
}

func (c *GH) EnableAutoMerge(ctx context.Context, prID, headOid, method string) error {
	return c.mutate(ctx, autoMergeMutation, map[string]any{"id": prID, "oid": headOid, "method": method})
}

func (c *GH) Comment(ctx context.Context, prID, body string) error {
	return c.mutate(ctx, commentMutation, map[string]any{"id": prID, "body": body})
}

func (c *GH) UpdateBody(ctx context.Context, prID, body string) error {
	return c.mutate(ctx, bodyMutation, map[string]any{"id": prID, "body": body})
}

func (c *GH) Close(ctx context.Context, prID string) error {
	return c.mutate(ctx, closeMutation, map[string]any{"id": prID})
}

func (c *GH) RequestReviews(ctx context.Context, prID string, userIDs, teamIDs []string) error {
	return c.mutate(ctx, reviewsMutation, map[string]any{"id": prID, "users": userIDs, "teams": teamIDs})
}

func (c *GH) RerunFailedJobs(ctx context.Context, repo string, runID int64) error {
	resp, err := c.rest.RequestWithContext(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/runs/%d/rerun-failed-jobs", repo, runID), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *GH) ReviewerID(ctx context.Context, reviewer string) (string, bool, error) {
	if org, slug, ok := strings.Cut(reviewer, "/"); ok {
		var resp struct {
			Organization *struct{ Team *struct{ ID string } }
		}
		if err := c.gql.DoWithContext(ctx, teamIDQuery, map[string]any{"org": org, "slug": slug}, &resp); err != nil {
			return "", true, notFound(err)
		}
		if resp.Organization == nil || resp.Organization.Team == nil {
			return "", true, fmt.Errorf("%w: team %s", ErrReviewerNotFound, reviewer)
		}
		return resp.Organization.Team.ID, true, nil
	}
	var resp struct{ User *struct{ ID string } }
	if err := c.gql.DoWithContext(ctx, userIDQuery, map[string]any{"login": reviewer}, &resp); err != nil {
		return "", false, notFound(err)
	}
	if resp.User == nil {
		return "", false, fmt.Errorf("%w: user %s", ErrReviewerNotFound, reviewer)
	}
	return resp.User.ID, false, nil
}

// notFound maps a GraphQL NOT_FOUND error to ErrReviewerNotFound.
func notFound(err error) error {
	var gqlErr *api.GraphQLError
	if errors.As(err, &gqlErr) {
		for _, e := range gqlErr.Errors {
			if e.Type == "NOT_FOUND" {
				return fmt.Errorf("%w: %s", ErrReviewerNotFound, e.Message)
			}
		}
	}
	return err
}
