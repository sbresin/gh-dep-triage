package triage

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
	}{
		{"acme/api#12", Ref{Raw: "acme/api#12", Kind: RefPR, PR: model.PRRef{Repo: "acme/api", Number: 12}}},
		{"group:lodash@4.17.21", Ref{Raw: "group:lodash@4.17.21", Kind: RefGroup, Package: "lodash", Target: "4.17.21"}},
		{"group:@types/node@20.1.0", Ref{Raw: "group:@types/node@20.1.0", Kind: RefGroup, Package: "@types/node", Target: "20.1.0"}},
		{"group:ESLint@9.1.0~major", Ref{Raw: "group:ESLint@9.1.0~major", Kind: RefGroup, Package: "eslint", Target: "9.1.0", Bump: "major"}},
		{"group:react-monorepo@19", Ref{Raw: "group:react-monorepo@19", Kind: RefGroup, Package: "react-monorepo", Target: "19"}},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if err != nil {
			t.Errorf("ParseRef(%q) error: %v", tt.in, err)
			continue
		}
		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("ParseRef(%q) (-want +got):\n%s", tt.in, diff)
		}
	}
}

func TestParseRefInvalid(t *testing.T) {
	for _, in := range []string{"", "acme/api", "acme/api#0", "group:lodash", "group:@types/node", "group:lodash@", "group:lodash@1.0~huge", "group:lodash@~major"} {
		_, err := ParseRef(in)
		var re *RefError
		if !errors.As(err, &re) || re.Code != "invalid_ref" {
			t.Errorf("ParseRef(%q) = %v, want invalid_ref", in, err)
		}
	}
}

func TestResolveGroup(t *testing.T) {
	groups := GroupPRs([]*model.PR{
		testPR("a/x", 1, "Bump eslint from 8.0.0 to 9.1.0"),
		testPR("a/y", 2, "Bump eslint from 9.0.0 to 9.1.0"),
		testPR("a/x", 3, "Bump lodash from 4.17.20 to 4.17.21"),
		testPR("a/x", 4, "chore(deps): update react monorepo to v19"),
	})
	resolve := func(s string) (*model.Group, error) {
		r, err := ParseRef(s)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", s, err)
		}
		return ResolveGroup(groups, r)
	}

	for _, s := range []string{"group:lodash@4.17.21", "group:lodash@4.17.21~patch"} {
		if g, err := resolve(s); err != nil || g.ID != "group:lodash@4.17.21" {
			t.Errorf("%s → %v, %v", s, g, err)
		}
	}
	if g, err := resolve("group:eslint@9.1.0~minor"); err != nil || g.ID != "group:eslint@9.1.0~minor" {
		t.Errorf("suffixed eslint → %v, %v", g, err)
	}
	if g, err := resolve("group:react-monorepo@19"); err != nil || len(g.PRs) != 1 {
		t.Errorf("slugged ref → %v, %v", g, err)
	}

	_, err := resolve("group:eslint@9.1.0")
	var re *RefError
	if !errors.As(err, &re) || re.Code != "ambiguous_ref" {
		t.Fatalf("want ambiguous_ref, got %v", err)
	}
	if diff := cmp.Diff([]string{"group:eslint@9.1.0~major", "group:eslint@9.1.0~minor"}, re.Candidates); diff != "" {
		t.Errorf("candidates (-want +got):\n%s", diff)
	}

	_, err = resolve("group:lodash@9.9.9")
	if !errors.As(err, &re) || re.Code != "not_found" {
		t.Errorf("want not_found, got %v", err)
	}
}

func TestParseRefRangeTargets(t *testing.T) {
	tests := []struct{ ref, target, bump string }{
		{"group:mypy@>=2.1,<2.5", ">=2.1,<2.5", ""},
		{"group:rails@~> 7.0", "~> 7.0", ""},
		{"group:rails@~> 7.0~major", "~> 7.0", "major"},
		{"group:requests@~=2.31", "~=2.31", ""},
	}
	for _, tt := range tests {
		r, err := ParseRef(tt.ref)
		if err != nil || r.Target != tt.target || r.Bump != tt.bump {
			t.Errorf("ParseRef(%q) = %+v, %v; want target %q bump %q", tt.ref, r, err, tt.target, tt.bump)
		}
	}
	if _, err := ParseRef("group:lodash@4.17.21~huge"); err == nil {
		t.Error("an unknown suffix after a version must still be rejected")
	}
}
