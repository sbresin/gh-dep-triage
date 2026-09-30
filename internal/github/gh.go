package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

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
		return "", err
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
