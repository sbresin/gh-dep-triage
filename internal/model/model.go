// Package model holds the data types shared by loading, triage and output.
package model

import (
	"fmt"
	"slices"
	"strconv"
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
	MergeQueue         bool         `json:"mergeQueue"`
	RequestedForReview bool         `json:"requestedForReview"`
	ViewerApproved     bool         `json:"viewerApproved"`
	RequestedReviewers []string     `json:"requestedReviewers"`
	Package            string       `json:"package"`
	SourceVersion      string       `json:"sourceVersion"`
	TargetVersion      string       `json:"targetVersion"`
	Bump               string       `json:"bump"`
	Directory          string       `json:"directory,omitempty"`
	GroupID            string       `json:"groupId,omitempty"`
	Checks             CheckSummary `json:"checks"`
	Blockers           []Blocker    `json:"blockers"`
	Status             Status       `json:"status"`
	MergeDenied        string       `json:"mergeDenied,omitempty"`
	Risk               *Risk        `json:"risk,omitempty"`

	PackageKey           string       `json:"-"`
	State                string       `json:"-"`
	IsDraft              bool         `json:"-"`
	SeenInReviewedSearch bool         `json:"-"`
	Body                 string       `json:"-"`
	ViewerReviews        []Review     `json:"-"`
	CheckRuns            []Check      `json:"-"`
	RepoSettings         RepoSettings `json:"-"`
	ID                   string       `json:"-"`
	HeadRefName          string       `json:"-"`
}

func (p *PR) PRRef() PRRef { return PRRef{Repo: p.Repo, Number: p.Number} }

// HasBlocker reports whether the PR has a blocker with this code.
func (p *PR) HasBlocker(code string) bool {
	for _, b := range p.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

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

const (
	ActionApprove       = "approve"
	ActionMerge         = "merge"
	ActionRebase        = "rebase"
	ActionRecreate      = "recreate"
	ActionRerun         = "rerun"
	ActionRequestReview = "request-review"
	ActionClose         = "close"
)

const (
	ResultPlanned = "planned"
	ResultSuccess = "success"
	ResultSkipped = "skipped"
	ResultFailed  = "failed"
	ResultDenied  = "denied"
)

const (
	ReasonHeadChanged     = "head_changed"
	ReasonNotEligible     = "not_eligible"
	ReasonChecksFailing   = "checks_failing"
	ReasonNotMergeable    = "not_mergeable"
	ReasonAlreadyMerging  = "already_merging"
	ReasonAlreadyApproved = "already_approved"
	ReasonMergeQueue      = "merge_queue"
	ReasonNoMergeMethod   = "no_merge_method"
	ReasonRefetchFailed   = "refetch_failed"
	ReasonMutationFailed  = "mutation_failed"
	ReasonCancelled       = "cancelled"
	ReasonNotOpen         = "not_open"
	ReasonNotBotPR        = "not_bot_pr"
	ReasonRepoDenied      = "repo_denied"
	ReasonRepoNotAllowed  = "repo_not_allowed"
	ReasonMajorNeedsFlag  = "major_requires_allow_major"

	ReasonNoRebaseCheckbox = "no_rebase_checkbox"
	ReasonAlreadyRequested = "already_requested"
	ReasonUnsupportedBot   = "unsupported_bot"
	ReasonNoFailedChecks   = "no_failed_checks"
	ReasonNotRerunnable    = "not_rerunnable"
	ReasonReviewerNotFound = "reviewer_not_found"
	ReasonBlockerMissing   = "blocker_missing"
	ReasonMalicious        = "malicious_package"
)

// Result is the outcome of one planned (action, PR) pair.
type Result struct {
	Action  string            `json:"action"`
	Ref     string            `json:"ref"`
	Args    map[string]string `json:"args,omitempty"`
	Status  string            `json:"status"`
	Reason  string            `json:"reason,omitempty"`
	Message string            `json:"message,omitempty"`
	Steps   []string          `json:"steps"`
	HeadOid string            `json:"headOid,omitempty"`
}

// DepKey identifies a package version on deps.dev. System "" with Name
// "github.com/<owner>/<repo>" is a project-only lookup (GitHub Actions).
type DepKey struct{ System, Name, Version string }

// FindingMalicious is the deps.dev finding that blocks approve and merge.
const FindingMalicious = "MALICIOUS"

// FindingCooldown marks a version published too recently to trust yet.
const FindingCooldown = "COOLDOWN"

// Risk is deps.dev data about a PR's target version and its source project.
type Risk struct {
	System      string    `json:"system,omitempty"`
	SourceRepo  string    `json:"sourceRepo,omitempty"`
	Stars       int       `json:"stars,omitempty"`
	Scorecard   float64   `json:"scorecard,omitempty"`
	PublishedAt time.Time `json:"publishedAt,omitzero"`
	Deprecated  bool      `json:"deprecated,omitempty"`
	Advisories  []string  `json:"advisories,omitempty"`
	Findings    []string  `json:"findings,omitempty"`
	CooldownEnd time.Time `json:"cooldownEnd,omitzero"` // when a COOLDOWN finding expires
}

func (r *Risk) HasFinding(t string) bool { return r != nil && slices.Contains(r.Findings, t) }

// Label is the first finding (a COOLDOWN with its time left) and the
// project's stars and scorecard, joined by " · "; "" when there is no data.
func (r *Risk) Label(now time.Time) string {
	if r == nil {
		return ""
	}
	var parts []string
	if len(r.Findings) > 0 {
		f := r.Findings[0]
		if f == FindingCooldown && r.CooldownEnd.After(now) {
			f += " " + timeLeft(r.CooldownEnd.Sub(now))
		}
		parts = append(parts, f)
	}
	if r.Stars > 0 {
		stars := strconv.Itoa(r.Stars)
		if r.Stars >= 1000 {
			stars = strconv.Itoa(r.Stars/1000) + "k"
		}
		stars += "★"
		if r.Scorecard > 0 {
			stars += fmt.Sprintf(" %.1f", r.Scorecard)
		}
		parts = append(parts, stars)
	}
	return strings.Join(parts, " · ")
}

// timeLeft is a short rounded duration: "<1h", "6h", "3d".
func timeLeft(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "<1h"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Round(time.Hour)/time.Hour)) + "h"
	default:
		return strconv.Itoa(int(d.Round(24*time.Hour)/(24*time.Hour))) + "d"
	}
}
