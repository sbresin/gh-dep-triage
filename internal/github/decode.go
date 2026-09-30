package github

import (
	"fmt"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

type rawActor struct {
	Typename     string `json:"__typename"`
	Login        string
	CombinedSlug string
}

type rawSearch struct {
	Search struct {
		PageInfo struct {
			HasNextPage bool
			EndCursor   string
		}
		Nodes []struct {
			Number     int
			Title      string
			URL        string
			Repository struct{ NameWithOwner string }
			Author     *rawActor
		}
	}
}

func (r rawSearch) hits() []SearchHit {
	out := []SearchHit{}
	for _, n := range r.Search.Nodes {
		if n.Number == 0 {
			continue
		}
		h := SearchHit{Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, URL: n.URL}
		if n.Author != nil {
			h.AuthorLogin, h.AuthorType = n.Author.Login, n.Author.Typename
		}
		out = append(out, h)
	}
	return out
}

type rawRepo struct {
	NameWithOwner            string
	MergeCommitAllowed       bool
	SquashMergeAllowed       bool
	RebaseMergeAllowed       bool
	AutoMergeAllowed         bool
	ViewerDefaultMergeMethod string
	PullRequest              *rawPR
}

type rawPR struct {
	Number           int
	Title            string
	URL              string
	Body             string
	State            string
	IsDraft          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	HeadRefOid       string
	BaseRefName      string
	MergeStateStatus string
	Mergeable        string
	ReviewDecision   string
	Author           *rawActor
	AutoMergeRequest *struct{ EnabledAt time.Time }
	Reviews          struct {
		Nodes []struct {
			State       string
			SubmittedAt *time.Time
		}
	}
	ReviewRequests struct {
		Nodes []struct{ RequestedReviewer *rawActor }
	}
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct{ Nodes []rawContext }
				}
			}
		}
	}
}

type rawContext struct {
	Typename   string `json:"__typename"`
	Name       string
	Status     string
	Conclusion string
	DetailsURL string
	DatabaseID int64
	IsRequired bool
	CheckSuite *struct {
		WorkflowRun *struct{ DatabaseID int64 }
	}
	Context   string
	State     string
	TargetURL string
}

func decodePRBatch(data map[string]*rawRepo, refs []model.PRRef, errByAlias map[string]string) ([]*model.PR, []model.Problem) {
	prs := []*model.PR{}
	warnings := []model.Problem{}
	for i, ref := range refs {
		alias := fmt.Sprintf("pr%d", i)
		if msg, ok := errByAlias[alias]; ok {
			warnings = append(warnings, model.Problem{Code: "fetch_failed", Ref: ref.String(), Message: msg})
			continue
		}
		repo := data[alias]
		if repo == nil || repo.PullRequest == nil {
			warnings = append(warnings, model.Problem{Code: "not_found", Ref: ref.String(), Message: "pull request not found or not accessible"})
			continue
		}
		prs = append(prs, toPR(ref, repo))
	}
	return prs, warnings
}

func toPR(ref model.PRRef, repo *rawRepo) *model.PR {
	raw := repo.PullRequest
	pr := &model.PR{
		Repo: ref.Repo, Number: ref.Number, Ref: ref.String(),
		Title: raw.Title, URL: raw.URL, Body: raw.Body, State: raw.State, IsDraft: raw.IsDraft,
		CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt,
		HeadOid: raw.HeadRefOid, BaseRef: raw.BaseRefName,
		MergeStateStatus: raw.MergeStateStatus, Mergeable: raw.Mergeable, ReviewDecision: raw.ReviewDecision,
		AutoMerge:          raw.AutoMergeRequest != nil,
		RequestedReviewers: []string{},
		ViewerReviews:      []model.Review{},
		CheckRuns:          []model.Check{},
		RepoSettings: model.RepoSettings{
			MergeCommitAllowed:       repo.MergeCommitAllowed,
			SquashMergeAllowed:       repo.SquashMergeAllowed,
			RebaseMergeAllowed:       repo.RebaseMergeAllowed,
			AutoMergeAllowed:         repo.AutoMergeAllowed,
			ViewerDefaultMergeMethod: repo.ViewerDefaultMergeMethod,
		},
	}
	if raw.Author != nil {
		pr.Author = raw.Author.Login
	}
	for _, r := range raw.Reviews.Nodes {
		rv := model.Review{State: r.State}
		if r.SubmittedAt != nil {
			rv.SubmittedAt = *r.SubmittedAt
		}
		pr.ViewerReviews = append(pr.ViewerReviews, rv)
	}
	for _, n := range raw.ReviewRequests.Nodes {
		if n.RequestedReviewer == nil {
			continue
		}
		name := n.RequestedReviewer.CombinedSlug
		if name == "" {
			name = n.RequestedReviewer.Login
		}
		if name != "" {
			pr.RequestedReviewers = append(pr.RequestedReviewers, name)
		}
	}
	for _, c := range raw.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, ctx := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			pr.CheckRuns = append(pr.CheckRuns, toCheck(ctx))
		}
	}
	return pr
}

func toCheck(c rawContext) model.Check {
	if c.Typename == "StatusContext" {
		return model.Check{Name: c.Context, Kind: model.CheckKindStatus, Conclusion: c.State, Required: c.IsRequired, URL: c.TargetURL}
	}
	ch := model.Check{Name: c.Name, Kind: model.CheckKindRun, Status: c.Status, Conclusion: c.Conclusion,
		Required: c.IsRequired, URL: c.DetailsURL, JobID: c.DatabaseID}
	if c.CheckSuite != nil && c.CheckSuite.WorkflowRun != nil {
		ch.WorkflowRunID = c.CheckSuite.WorkflowRun.DatabaseID
	}
	return ch
}
