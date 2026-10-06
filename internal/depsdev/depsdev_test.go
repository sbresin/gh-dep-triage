package depsdev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// server answers each path with the queued bodies in order (the last one
// repeats) and records every request body.
type server struct {
	mu     sync.Mutex
	bodies map[string][]string
	status map[string][]int
	got    map[string][]map[string]any
}

func newServer(t *testing.T) (*server, *Client) {
	s := &server{bodies: map[string][]string{}, status: map[string][]int{}, got: map[string][]map[string]any{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		s.got[r.URL.Path] = append(s.got[r.URL.Path], req)
		n := len(s.got[r.URL.Path]) - 1
		if st := s.status[r.URL.Path]; len(st) > 0 {
			if code := st[min(n, len(st)-1)]; code != http.StatusOK {
				w.WriteHeader(code)
				return
			}
		}
		b := s.bodies[r.URL.Path]
		if len(b) == 0 {
			_, _ = io.WriteString(w, `{"responses":[]}`)
			return
		}
		_, _ = io.WriteString(w, b[min(n, len(b)-1)])
	}))
	t.Cleanup(ts.Close)
	c := &Client{BaseURL: ts.URL, HTTP: ts.Client(), Sleep: func(context.Context, time.Duration) error { return nil }}
	return s, c
}

var lodash = model.DepKey{System: "NPM", Name: "lodash", Version: "4.17.21"}
var checkout = model.DepKey{Name: "github.com/actions/checkout"}

func TestLookup(t *testing.T) {
	s, c := newServer(t)
	s.bodies["/versionbatch"] = []string{`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},
		"version":{"publishedAt":"2021-02-20T15:42:16Z","isDeprecated":false,"advisoryKeys":[{"id":"GHSA-1"}],
		"relatedProjects":[{"projectKey":{"id":"github.com/lodash/issues"},"relationType":"ISSUE_TRACKER"},
		                   {"projectKey":{"id":"github.com/lodash/lodash"},"relationType":"SOURCE_REPO"}]}}]}`}
	s.bodies["/findingsbatch"] = []string{`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},
		"findings":{"requestedVersion":{"findings":[{"type":"REMEDIATION"},{"type":"COOLDOWN","cooldownContext":{"end":"2026-10-06T19:31:20Z"}}]},"packageFindings":[{"type":"COOLDOWN"}]}}]}`}
	s.bodies["/projectbatch"] = []string{`{"responses":[
		{"request":{"projectKey":{"id":"github.com/lodash/lodash"}},"project":{"starsCount":61277,"scorecard":{"overallScore":7.5}}},
		{"request":{"projectKey":{"id":"github.com/actions/checkout"}},"project":{"starsCount":7000,"scorecard":{"overallScore":6.1}}}]}`}

	got, err := c.Lookup(context.Background(), []model.DepKey{lodash, lodash, checkout})
	if err != nil {
		t.Fatal(err)
	}
	want := map[model.DepKey]model.Risk{
		lodash: {System: "NPM", SourceRepo: "github.com/lodash/lodash", Stars: 61277, Scorecard: 7.5,
			PublishedAt: time.Date(2021, 2, 20, 15, 42, 16, 0, time.UTC), Advisories: []string{"GHSA-1"}, Findings: []string{"COOLDOWN"},
			CooldownEnd: time.Date(2026, 10, 6, 19, 31, 20, 0, time.UTC)},
		checkout: {SourceRepo: "github.com/actions/checkout", Stars: 7000, Scorecard: 6.1},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if n := len(s.got["/versionbatch"][0]["requests"].([]any)); n != 1 {
		t.Errorf("versionbatch got %d requests, want 1 (deduplicated)", n)
	}
	var ids []string
	for _, r := range s.got["/projectbatch"][0]["requests"].([]any) {
		ids = append(ids, r.(map[string]any)["projectKey"].(map[string]any)["id"].(string))
	}
	sort.Strings(ids)
	if !cmp.Equal(ids, []string{"github.com/actions/checkout", "github.com/lodash/lodash"}) {
		t.Errorf("projectbatch ids = %v", ids)
	}
}

func TestLookupNotFound(t *testing.T) {
	s, c := newServer(t)
	s.bodies["/findingsbatch"] = []string{`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},
		"findings":{"packageFindings":[{"type":"NOT_FOUND"}]}}]}`}
	got, err := c.Lookup(context.Background(), []model.DepKey{lodash})
	if err != nil || !cmp.Equal(got[lodash], model.Risk{System: "NPM", Findings: []string{"NOT_FOUND"}}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestLookupFollowsPageTokens(t *testing.T) {
	s, c := newServer(t)
	s.bodies["/findingsbatch"] = []string{
		`{"responses":[],"nextPageToken":"p2"}`,
		`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},"findings":{"packageFindings":[{"type":"DEPRECATED"}]}}]}`,
	}
	got, err := c.Lookup(context.Background(), []model.DepKey{lodash})
	if err != nil || !cmp.Equal(got[lodash].Findings, []string{"DEPRECATED"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	calls := s.got["/findingsbatch"]
	if len(calls) != 2 || calls[1]["pageToken"] != "p2" || calls[0]["pageToken"] != nil {
		t.Errorf("findingsbatch calls = %v", calls)
	}
}

func TestLookupRetriesRateLimit(t *testing.T) {
	s, c := newServer(t)
	s.status["/versionbatch"] = []int{http.StatusTooManyRequests, http.StatusOK}
	if _, err := c.Lookup(context.Background(), []model.DepKey{lodash}); err != nil || len(s.got["/versionbatch"]) != 2 {
		t.Errorf("err=%v calls=%d, want a retry then success", err, len(s.got["/versionbatch"]))
	}
}

func TestLookupFailsAfterRetries(t *testing.T) {
	s, c := newServer(t)
	s.status["/findingsbatch"] = []int{http.StatusInternalServerError}
	if _, err := c.Lookup(context.Background(), []model.DepKey{lodash}); err == nil || len(s.got["/findingsbatch"]) != 4 {
		t.Errorf("err=%v calls=%d, want an error after 1+3 attempts", err, len(s.got["/findingsbatch"]))
	}
}

func TestLookupNoKeysMakesNoRequests(t *testing.T) {
	s, c := newServer(t)
	if got, err := c.Lookup(context.Background(), nil); err != nil || len(got) != 0 || len(s.got) != 0 {
		t.Errorf("got %v, %v, requests %v", got, err, s.got)
	}
}

// A failed batch must not discard the safety findings that did arrive.
func TestLookupKeepsFindingsWhenProjectsFail(t *testing.T) {
	s, c := newServer(t)
	s.bodies["/versionbatch"] = []string{`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},
		"version":{"relatedProjects":[{"projectKey":{"id":"github.com/lodash/lodash"},"relationType":"SOURCE_REPO"}]}}]}`}
	s.bodies["/findingsbatch"] = []string{`{"responses":[{"request":{"versionKey":{"system":"NPM","name":"lodash","version":"4.17.21"}},
		"findings":{"packageFindings":[{"type":"MALICIOUS"}]}}]}`}
	s.status["/projectbatch"] = []int{http.StatusInternalServerError}
	got, err := c.Lookup(context.Background(), []model.DepKey{lodash})
	if err == nil || !slices.Contains(got[lodash].Findings, model.FindingMalicious) {
		t.Errorf("got %+v, %v; want the MALICIOUS finding and an error", got, err)
	}
}
