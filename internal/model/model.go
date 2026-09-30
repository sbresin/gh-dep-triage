// Package model holds the data types shared by loading, triage and output.
package model

import (
	"fmt"
	"strings"
	"time"
)

// SchemaVersion is the version of the --json output contract.
const SchemaVersion = 1

type Status string

const (
	StatusReady   Status = "ready"
	StatusMerging Status = "merging"
	StatusBlocked Status = "blocked"
)

const (
	BumpMajor   = "major"
	BumpMinor   = "minor"
	BumpPatch   = "patch"
	BumpUnknown = "unknown"
)

const (
	CheckKindRun    = "check_run"
	CheckKindStatus = "status"
)

const (
	CheckStatePassed  = "passed"
	CheckStateFailed  = "failed"
	CheckStatePending = "pending"
	CheckStateSkipped = "skipped"
)

const (
	BlockerChecksFailing    = "checks_failing"
	BlockerBehindBase       = "behind_base"
	BlockerConflicts        = "conflicts"
	BlockerReviewRequired   = "review_required"
	BlockerChangesRequested = "changes_requested"
	BlockerSuperseded       = "superseded"
	BlockerStale            = "stale"
	BlockerBlockedUnknown   = "blocked_unknown"
)

type PRRef struct {
	Repo   string
	Number int
}

func (r PRRef) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

type Review struct {
	State       string
	SubmittedAt time.Time
}

// Check is one status-check-rollup context. For CheckKindStatus the commit
// status state is stored in Conclusion. State is set by triage.
type Check struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Status        string `json:"status,omitempty"`
	Conclusion    string `json:"conclusion,omitempty"`
	State         string `json:"state"`
	Required      bool   `json:"required"`
	URL           string `json:"url,omitempty"`
	JobID         int64  `json:"jobId,omitempty"`
	WorkflowRunID int64  `json:"workflowRunId,omitempty"`
}

type CheckSummary struct {
	Total        int      `json:"total"`
	Passed       int      `json:"passed"`
	Skipped      int      `json:"skipped"`
	Failed       int      `json:"failed"`
	Pending      int      `json:"pending"`
	FailedNames  []string `json:"failedNames"`
	PendingNames []string `json:"pendingNames"`
}

type SuggestedAction struct {
	Action   string `json:"action"`
	Command  string `json:"command,omitempty"`
	Checkout string `json:"checkout,omitempty"`
}

type Blocker struct {
	Code             string            `json:"code"`
	Detail           string            `json:"detail"`
	SuggestedActions []SuggestedAction `json:"suggestedActions"`
}

type RepoSettings struct {
	MergeCommitAllowed       bool
	SquashMergeAllowed       bool
	RebaseMergeAllowed       bool
	AutoMergeAllowed         bool
	ViewerDefaultMergeMethod string
}

type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type PR struct {
	Repo               string       `json:"repo"`
	Number             int          `json:"number"`
	Ref                string       `json:"ref"`
	Title              string       `json:"title"`
	URL                string       `json:"url"`
	Author             string       `json:"author"`
	CreatedAt          time.Time    `json:"createdAt"`
	UpdatedAt          time.Time    `json:"updatedAt"`
	HeadOid            string       `json:"headOid"`
	BaseRef            string       `json:"baseRef"`
	MergeStateStatus   string       `json:"mergeStateStatus"`
	Mergeable          string       `json:"mergeable"`
	ReviewDecision     string       `json:"reviewDecision"`
	AutoMerge          bool         `json:"autoMerge"`
	RequestedForReview bool         `json:"requestedForReview"`
	ViewerApproved     bool         `json:"viewerApproved"`
	RequestedReviewers []string     `json:"requestedReviewers"`
	Package            string       `json:"package"`
	SourceVersion      string       `json:"sourceVersion"`
	TargetVersion      string       `json:"targetVersion"`
	Bump               string       `json:"bump"`
	GroupID            string       `json:"groupId,omitempty"`
	Checks             CheckSummary `json:"checks"`
	Blockers           []Blocker    `json:"blockers"`
	Status             Status       `json:"status"`

	PackageKey           string       `json:"-"`
	State                string       `json:"-"`
	IsDraft              bool         `json:"-"`
	SeenInReviewedSearch bool         `json:"-"`
	Body                 string       `json:"-"`
	ViewerReviews        []Review     `json:"-"`
	CheckRuns            []Check      `json:"-"`
	RepoSettings         RepoSettings `json:"-"`
}

func (p *PR) PRRef() PRRef { return PRRef{Repo: p.Repo, Number: p.Number} }

type Group struct {
	ID            string `json:"id,omitempty"`
	Package       string `json:"package"`
	TargetVersion string `json:"targetVersion"`
	Bump          string `json:"bump"`
	PRs           []*PR  `json:"prs"`

	PackageKey string `json:"-"`
}

type Problem struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Ref        string   `json:"ref,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

type Snapshot struct {
	Viewer      string
	GeneratedAt time.Time
	Groups      []*Group
	Warnings    []Problem
}

func (s *Snapshot) PRs() []*PR {
	prs := []*PR{}
	for _, g := range s.Groups {
		prs = append(prs, g.PRs...)
	}
	return prs
}

func (s *Snapshot) Find(ref PRRef) *PR {
	for _, pr := range s.PRs() {
		if pr.Number == ref.Number && strings.EqualFold(pr.Repo, ref.Repo) {
			return pr
		}
	}
	return nil
}
