// Package github talks to the GitHub API through go-gh.
package github

import (
	"context"
	"errors"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var ErrNotAuthenticated = errors.New("not authenticated with GitHub; run `gh auth login`")

// ErrReviewerNotFound is returned by ReviewerID for an unknown user or team.
var ErrReviewerNotFound = errors.New("reviewer not found")

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
	// Approve submits an APPROVE review on headOid.
	Approve(ctx context.Context, prID, headOid string) error
	// Merge merges now; GitHub rejects it if the head is no longer headOid.
	Merge(ctx context.Context, prID, headOid, method string) error
	// EnableAutoMerge turns on auto-merge pinned to headOid.
	EnableAutoMerge(ctx context.Context, prID, headOid, method string) error
	// Comment adds a comment to the PR.
	Comment(ctx context.Context, prID, body string) error
	// UpdateBody replaces the PR description.
	UpdateBody(ctx context.Context, prID, body string) error
	// RerunFailedJobs re-runs the failed jobs of one Actions workflow run.
	RerunFailedJobs(ctx context.Context, repo string, runID int64) error
	// ReviewerID resolves a user login or org/team to a node ID; team
	// reports which. Unknown reviewers give ErrReviewerNotFound.
	ReviewerID(ctx context.Context, reviewer string) (id string, team bool, err error)
	// RequestReviews adds review requests, keeping the existing ones.
	RequestReviews(ctx context.Context, prID string, userIDs, teamIDs []string) error
	// Close closes the PR without merging.
	Close(ctx context.Context, prID string) error
}
