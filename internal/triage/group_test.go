package triage

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func ids(groups []*model.Group) []string {
	out := []string{}
	for _, g := range groups {
		out = append(out, g.ID)
	}
	return out
}

func TestInferBumps(t *testing.T) {
	known := testPR("a/x", 1, "Bump react from 18.3.0 to 19.0.0")
	unknown := testPR("a/y", 2, "update dependency react to v19.0.0")
	InferBumps([]*model.PR{known, unknown})
	if unknown.Bump != model.BumpMajor {
		t.Errorf("inferred = %q, want major", unknown.Bump)
	}

	major := testPR("a/x", 1, "Bump eslint from 8.0.0 to 9.1.0")
	minor := testPR("a/y", 2, "Bump eslint from 9.0.0 to 9.1.0")
	amb := testPR("a/z", 3, "update dependency eslint to v9.1.0")
	InferBumps([]*model.PR{major, minor, amb})
	if amb.Bump != model.BumpUnknown {
		t.Errorf("conflicting known bumps must leave unknown, got %q", amb.Bump)
	}
}

func TestGroupPRsOrderAndMembers(t *testing.T) {
	prs := []*model.PR{
		testPR("acme/web", 2, "Bump lodash from 4.17.20 to 4.17.21"),
		testPR("acme/api", 1, "Bump lodash from 4.17.20 to 4.17.21"),
		testPR("acme/api", 3, "Bump axios from 1.6.0 to 1.7.0"),
	}
	groups := GroupPRs(prs)
	if diff := cmp.Diff([]string{"group:axios@1.7.0", "group:lodash@4.17.21"}, ids(groups)); diff != "" {
		t.Errorf("ids (-want +got):\n%s", diff)
	}
	lodash := groups[1]
	if lodash.PRs[0].Ref != "acme/api#1" || lodash.PRs[1].Ref != "acme/web#2" {
		t.Errorf("PR order: %s, %s", lodash.PRs[0].Ref, lodash.PRs[1].Ref)
	}
	if lodash.PRs[1].GroupID != "group:lodash@4.17.21" {
		t.Errorf("GroupID not set on PR: %q", lodash.PRs[1].GroupID)
	}
}

func TestGroupIDs(t *testing.T) {
	prs := []*model.PR{
		testPR("a/x", 1, "Bump eslint from 8.0.0 to 9.1.0"),
		testPR("a/y", 2, "Bump eslint from 9.0.0 to 9.1.0"),
		testPR("a/x", 3, "Bump @types/node from 20.0.0 to 20.1.0"),
		testPR("a/x", 4, "chore(deps): update react monorepo to v19"),
		testPR("a/x", 5, "Some random title"),
	}
	got := ids(GroupPRs(prs))
	want := []string{
		"group:@types/node@20.1.0",
		"group:eslint@9.1.0~major",
		"group:eslint@9.1.0~minor",
		"group:react-monorepo@19",
		"",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
