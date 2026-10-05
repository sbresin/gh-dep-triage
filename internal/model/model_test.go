package model

import (
	"encoding/json"
	"testing"
)

func TestPRRefString(t *testing.T) {
	if got := (PRRef{Repo: "acme/api", Number: 12}).String(); got != "acme/api#12" {
		t.Errorf("got %q", got)
	}
}

func TestSnapshotFindIsCaseInsensitiveOnRepo(t *testing.T) {
	a := &PR{Repo: "acme/api", Number: 1}
	b := &PR{Repo: "acme/web", Number: 2}
	s := &Snapshot{Groups: []*Group{{PRs: []*PR{a}}, {PRs: []*PR{b}}}}
	if got := s.Find(PRRef{Repo: "Acme/Web", Number: 2}); got != b {
		t.Errorf("Find = %v, want b", got)
	}
	if got := s.Find(PRRef{Repo: "acme/api", Number: 9}); got != nil {
		t.Errorf("Find missing = %v, want nil", got)
	}
	if n := len(s.PRs()); n != 2 {
		t.Errorf("PRs() len = %d", n)
	}
}

func TestPRJSONHidesInternalFields(t *testing.T) {
	b, err := json.Marshal(PR{Repo: "acme/api", Number: 1, Body: "body", HeadOid: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"body", "Body", "CheckRuns", "ViewerReviews", "RepoSettings", "PackageKey", "State", "IsDraft"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected key %q in PR JSON", k)
		}
	}
	if m["headOid"] != "abc" {
		t.Errorf("headOid = %v", m["headOid"])
	}
}

func TestRiskLabel(t *testing.T) {
	tests := []struct {
		risk *Risk
		want string
	}{
		{nil, ""},
		{&Risk{System: "NPM"}, ""},
		{&Risk{Stars: 61277, Scorecard: 7.5}, "61k★ 7.5"},
		{&Risk{Stars: 420}, "420★"},
		{&Risk{Stars: 61277, Findings: []string{"COOLDOWN", "DEPRECATED"}}, "COOLDOWN"},
	}
	for _, tt := range tests {
		if got := tt.risk.Label(); got != tt.want {
			t.Errorf("Label(%+v) = %q, want %q", tt.risk, got, tt.want)
		}
	}
}
