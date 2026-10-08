package triage

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func refsOf(prs []*model.PR) []string {
	out := []string{}
	for _, p := range prs {
		out = append(out, p.Ref)
	}
	return out
}

func TestSortPRs(t *testing.T) {
	a := testPR("acme/web", 1, "Bump zod from 3.0.0 to 3.0.1") // patch, ok
	b := testPR("acme/api", 2, "Bump axios from 1.0.0 to 2.0.0", func(p *model.PR) {
		p.CheckRuns = []model.Check{checkRun("t", "IN_PROGRESS", "")}
	}) // major, pending
	c := testPR("acme/api", 3, "Bump lodash from 4.0.0 to 4.1.0", func(p *model.PR) {
		p.CheckRuns = []model.Check{checkRun("t", "COMPLETED", "FAILURE")}
	}) // minor, failed
	in := []*model.PR{a, b, c}

	tests := map[string][]string{
		"package":  {"acme/api#2", "acme/api#3", "acme/web#1"},
		"severity": {"acme/api#2", "acme/api#3", "acme/web#1"},
		"checks":   {"acme/api#3", "acme/api#2", "acme/web#1"},
		"repo":     {"acme/api#2", "acme/api#3", "acme/web#1"},
	}
	for mode, want := range tests {
		if diff := cmp.Diff(want, refsOf(SortPRs(in, mode))); diff != "" {
			t.Errorf("%s (-want +got):\n%s", mode, diff)
		}
	}
	if diff := cmp.Diff([]string{"acme/web#1", "acme/api#2", "acme/api#3"}, refsOf(in)); diff != "" {
		t.Errorf("input must not be reordered:\n%s", diff)
	}
	z := testPR("acme/api", 4, "Bump zod from 3.0.0 to 3.0.1")
	if got := refsOf(SortPRs([]*model.PR{a, z}, "severity")); !cmp.Equal(got, []string{"acme/api#4", "acme/web#1"}) {
		t.Errorf("severity tie-break by repo: %v", got)
	}
}
