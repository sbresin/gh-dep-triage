// Package github talks to the GitHub API through go-gh.
package github

import (
	"context"
	"errors"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var ErrNotAuthenticated = errors.New("not authenticated with GitHub; run `gh auth login`")

type SearchHit struct {
	Repo        string
	Number      int
	Title       string
	URL         string
	AuthorLogin string
	AuthorType  string
}

type Client interface {
	Viewer(ctx context.Context) (string, error)
	// SearchPRs returns at most limit hits for an issue-search query.
	SearchPRs(ctx context.Context, query string, limit int) ([]SearchHit, error)
	// FetchPRs fetches one batch (≤ BatchSize). Per-PR failures are warnings;
	// err is set only when the whole request failed.
	FetchPRs(ctx context.Context, viewer string, refs []model.PRRef) ([]*model.PR, []model.Problem, error)
	PRFiles(ctx context.Context, ref model.PRRef) ([]model.ChangedFile, error)
	JobLog(ctx context.Context, repo string, jobID int64) (string, error)
}
