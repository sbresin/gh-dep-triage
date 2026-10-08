// Package githubtest provides an in-memory github.Client for tests.
package githubtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

type Fake struct {
	ViewerLogin  string
	Requested    []github.SearchHit
	Reviewed     []github.SearchHit
	PRs          map[string]*model.PR
	Files        map[string][]model.ChangedFile
	Logs         map[int64]string
	FailBatch    map[string]error // a batch containing this ref fails as a whole
	FailOnce     map[string]error // like FailBatch, but only for the first batch containing this ref
	MutationErr  map[string]error
	AfterApprove map[string]func(*model.PR)
	Reviewers    map[string]string // reviewer → node ID; "T_" IDs are teams

	mu      sync.Mutex
	Queries []string
	Batches [][]model.PRRef
	Calls   []string
}

var _ github.Client = (*Fake)(nil)

func NewFake(viewer string) *Fake {
	return &Fake{ViewerLogin: viewer, PRs: map[string]*model.PR{}, Files: map[string][]model.ChangedFile{},
		Logs: map[int64]string{}, FailBatch: map[string]error{}, FailOnce: map[string]error{},
		MutationErr: map[string]error{}, AfterApprove: map[string]func(*model.PR){}, Reviewers: map[string]string{}}
}

// NewPR returns a raw (not enriched) open Dependabot PR created 2026-09-25.
func NewPR(repo string, n int, title string) *model.PR {
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	return &model.PR{
		Repo: repo, Number: n, Ref: fmt.Sprintf("%s#%d", repo, n), Title: title,
		URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, n), Author: "dependabot",
		State: "OPEN", CreatedAt: created, UpdatedAt: created,
		HeadOid: fmt.Sprintf("sha%d", n), BaseRef: "main",
		MergeStateStatus: "BLOCKED", Mergeable: "MERGEABLE", ReviewDecision: "REVIEW_REQUIRED",
		ID:           "PR_" + fmt.Sprintf("%s#%d", repo, n),
		RepoSettings: model.RepoSettings{SquashMergeAllowed: true, AutoMergeAllowed: true, ViewerDefaultMergeMethod: "SQUASH"},
	}
}

func CheckRun(name, status, conclusion string, jobID int64) model.Check {
	return model.Check{Name: name, Kind: model.CheckKindRun, Status: status, Conclusion: conclusion, JobID: jobID,
		URL: fmt.Sprintf("https://github.com/example/actions/runs/1/job/%d", jobID)}
}

func Hit(pr *model.PR) github.SearchHit {
	return github.SearchHit{Repo: pr.Repo, Number: pr.Number, Title: pr.Title, URL: pr.URL, AuthorLogin: pr.Author, AuthorType: "Bot"}
}

func (f *Fake) Add(pr *model.PR, requested, reviewed bool) {
	f.PRs[pr.Ref] = pr
	if requested {
		f.Requested = append(f.Requested, Hit(pr))
	}
	if reviewed {
		f.Reviewed = append(f.Reviewed, Hit(pr))
	}
}

func (f *Fake) Viewer(context.Context) (string, error) { return f.ViewerLogin, nil }

func (f *Fake) SearchPRs(_ context.Context, query string, limit int) ([]github.SearchHit, error) {
	f.mu.Lock()
	f.Queries = append(f.Queries, query)
	f.mu.Unlock()
	hits := f.Requested
	if strings.Contains(query, "reviewed-by:") {
		hits = f.Reviewed
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (f *Fake) FetchPRs(_ context.Context, _ string, refs []model.PRRef) ([]*model.PR, []model.Problem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Batches = append(f.Batches, append([]model.PRRef(nil), refs...))
	for _, r := range refs {
		if err := f.FailOnce[r.String()]; err != nil {
			delete(f.FailOnce, r.String())
			return nil, nil, err
		}
	}
	for _, r := range refs {
		if err := f.FailBatch[r.String()]; err != nil {
			return nil, nil, err
		}
	}
	prs := []*model.PR{}
	warnings := []model.Problem{}
	for _, r := range refs {
		p, ok := f.PRs[r.String()]
		if !ok {
			warnings = append(warnings, model.Problem{Code: "not_found", Ref: r.String(), Message: "pull request not found or not accessible"})
			continue
		}
		cp := *p
		cp.CheckRuns = append([]model.Check(nil), p.CheckRuns...)
		prs = append(prs, &cp)
	}
	return prs, warnings, nil
}

func (f *Fake) PRFiles(_ context.Context, ref model.PRRef) ([]model.ChangedFile, error) {
	return f.Files[ref.String()], nil
}

func (f *Fake) JobLog(_ context.Context, _ string, jobID int64) (string, error) {
	l, ok := f.Logs[jobID]
	if !ok {
		return "", fmt.Errorf("no log for job %d", jobID)
	}
	return l, nil
}

func (f *Fake) byID(id string) *model.PR {
	for _, p := range f.PRs {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (f *Fake) mutation(kind, id string, extra ...string) (*model.PR, error) {
	pr := f.byID(id)
	if pr == nil {
		return nil, fmt.Errorf("Could not resolve to a node with the global id of '%s'", id) //nolint:staticcheck // mirrors GitHub's error text
	}
	f.Calls = append(f.Calls, strings.Join(append([]string{kind, pr.Ref}, extra...), " "))
	if err := f.MutationErr[kind+" "+pr.Ref]; err != nil {
		return nil, err
	}
	return pr, nil
}

func (f *Fake) Approve(_ context.Context, prID, headOid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, err := f.mutation("approve", prID, headOid)
	if err != nil {
		return err
	}
	pr.ViewerReviews = append(pr.ViewerReviews, model.Review{State: "APPROVED", SubmittedAt: time.Now()})
	pr.ReviewDecision = "APPROVED"
	if hook := f.AfterApprove[pr.Ref]; hook != nil {
		hook(pr)
	}
	return nil
}

func (f *Fake) Merge(_ context.Context, prID, headOid, method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, err := f.mutation("merge", prID, headOid, method)
	if err != nil {
		return err
	}
	pr.State = "MERGED"
	return nil
}

func (f *Fake) EnableAutoMerge(_ context.Context, prID, headOid, method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, err := f.mutation("automerge", prID, headOid, method)
	if err != nil {
		return err
	}
	pr.AutoMerge = true
	return nil
}

func (f *Fake) Comment(_ context.Context, prID, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.mutation("comment", prID, body)
	return err
}

func (f *Fake) UpdateBody(_ context.Context, prID, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, err := f.mutation("body", prID)
	if err != nil {
		return err
	}
	pr.Body = body
	return nil
}

func (f *Fake) RerunFailedJobs(_ context.Context, repo string, runID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := fmt.Sprintf("rerun %s %d", repo, runID)
	f.Calls = append(f.Calls, call)
	return f.MutationErr[call]
}

func (f *Fake) ReviewerID(_ context.Context, reviewer string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.Reviewers[reviewer]
	if !ok {
		return "", false, fmt.Errorf("%w: %s", github.ErrReviewerNotFound, reviewer)
	}
	return id, strings.HasPrefix(id, "T_"), nil
}

func (f *Fake) RequestReviews(_ context.Context, prID string, userIDs, teamIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.mutation("request-review", prID, strings.Join(append(append([]string{}, userIDs...), teamIDs...), ","))
	return err
}

func (f *Fake) Close(_ context.Context, prID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, err := f.mutation("close", prID)
	if err != nil {
		return err
	}
	pr.State = "CLOSED"
	return nil
}
