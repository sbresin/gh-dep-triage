package githubtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestFakeMutations(t *testing.T) {
	ctx := context.Background()
	f := NewFake("octocat")
	pr := NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	f.Add(pr, true, false)
	f.AfterApprove["acme/api#1"] = func(p *model.PR) { p.MergeStateStatus = "CLEAN" }

	if err := f.Approve(ctx, pr.ID, "sha1"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := f.FetchPRs(ctx, "octocat", []model.PRRef{{Repo: "acme/api", Number: 1}})
	if got[0].ReviewDecision != "APPROVED" || got[0].MergeStateStatus != "CLEAN" || len(got[0].ViewerReviews) != 1 {
		t.Errorf("after approve: %+v", got[0])
	}
	if err := f.EnableAutoMerge(ctx, pr.ID, "sha1", "SQUASH"); err != nil {
		t.Fatal(err)
	}
	if err := f.Merge(ctx, pr.ID, "sha1", "SQUASH"); err != nil {
		t.Fatal(err)
	}
	want := []string{"approve acme/api#1 sha1", "automerge acme/api#1 sha1 SQUASH", "merge acme/api#1 sha1 SQUASH"}
	if diff := cmp.Diff(want, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if !f.PRs["acme/api#1"].AutoMerge || f.PRs["acme/api#1"].State != "MERGED" {
		t.Errorf("state: %+v", f.PRs["acme/api#1"])
	}

	f.MutationErr["merge acme/api#1"] = errors.New("boom")
	if err := f.Merge(ctx, pr.ID, "sha1", "SQUASH"); err == nil || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
	if err := f.Approve(ctx, "PR_unknown", "x"); err == nil {
		t.Error("unknown PR id must fail")
	}
}

func TestFakeUnblockMutations(t *testing.T) {
	ctx := context.Background()
	f := NewFake("octocat")
	pr := NewPR("acme/api", 1, "Bump a from 1.0.0 to 1.0.1")
	f.Add(pr, true, false)
	f.Reviewers["alice"] = "U_1"
	f.Reviewers["acme/platform"] = "T_1"

	if err := f.Comment(ctx, pr.ID, "@dependabot rebase"); err != nil {
		t.Fatal(err)
	}
	if err := f.UpdateBody(ctx, pr.ID, "ticked"); err != nil {
		t.Fatal(err)
	}
	if err := f.RerunFailedJobs(ctx, "acme/api", 42); err != nil {
		t.Fatal(err)
	}
	if id, team, err := f.ReviewerID(ctx, "acme/platform"); id != "T_1" || !team || err != nil {
		t.Errorf("team lookup: %q %v %v", id, team, err)
	}
	if _, _, err := f.ReviewerID(ctx, "ghost"); !errors.Is(err, github.ErrReviewerNotFound) {
		t.Errorf("ghost: %v", err)
	}
	if err := f.RequestReviews(ctx, pr.ID, []string{"U_1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(ctx, pr.ID); err != nil {
		t.Fatal(err)
	}
	want := []string{"comment acme/api#1 @dependabot rebase", "body acme/api#1", "rerun acme/api 42", "request-review acme/api#1 U_1", "close acme/api#1"}
	if diff := cmp.Diff(want, f.Calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if p := f.PRs["acme/api#1"]; p.Body != "ticked" || p.State != "CLOSED" {
		t.Errorf("state: body=%q state=%q", p.Body, p.State)
	}
	f.MutationErr["rerun acme/api 7"] = errors.New("boom")
	if err := f.RerunFailedJobs(ctx, "acme/api", 7); err == nil {
		t.Error("rerun error not returned")
	}
}
