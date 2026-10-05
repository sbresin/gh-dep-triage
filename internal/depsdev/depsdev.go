// Package depsdev looks up dependency risk data on deps.dev (Open Source Insights).
package depsdev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

const (
	DefaultBaseURL = "https://api.deps.dev/v3alpha"
	maxBatch       = 5000
	maxRetries     = 3
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Sleep   func(context.Context, time.Duration) error // nil sleeps for real
}

func New() *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

type versionKey struct {
	System  string `json:"system"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type finding struct {
	Type string `json:"type"`
}

type projectKey struct {
	ID string `json:"id"`
}

type versionPage struct {
	Responses []struct {
		Request struct {
			VersionKey versionKey `json:"versionKey"`
		} `json:"request"`
		Version *struct {
			PublishedAt  time.Time `json:"publishedAt"`
			IsDeprecated bool      `json:"isDeprecated"`
			AdvisoryKeys []struct {
				ID string `json:"id"`
			} `json:"advisoryKeys"`
			RelatedProjects []struct {
				ProjectKey   projectKey `json:"projectKey"`
				RelationType string     `json:"relationType"`
			} `json:"relatedProjects"`
		} `json:"version"`
	} `json:"responses"`
	NextPageToken string `json:"nextPageToken"`
}

type findingsPage struct {
	Responses []struct {
		Request struct {
			VersionKey versionKey `json:"versionKey"`
		} `json:"request"`
		Findings struct {
			RequestedVersion *struct {
				Findings []finding `json:"findings"`
			} `json:"requestedVersion"`
			PackageFindings []finding `json:"packageFindings"`
		} `json:"findings"`
	} `json:"responses"`
	NextPageToken string `json:"nextPageToken"`
}

type projectPage struct {
	Responses []struct {
		Request struct {
			ProjectKey projectKey `json:"projectKey"`
		} `json:"request"`
		Project *struct {
			StarsCount int `json:"starsCount"`
			Scorecard  *struct {
				OverallScore float64 `json:"overallScore"`
			} `json:"scorecard"`
		} `json:"project"`
	} `json:"responses"`
	NextPageToken string `json:"nextPageToken"`
}

// Lookup fetches risk data for keys in three batch calls: versions and
// findings in parallel, then the source projects. Version keys are always in
// the result; project-only keys only when deps.dev knows the project. On
// error the result still holds whatever arrived, so a failed project batch
// never discards findings such as MALICIOUS.
func (c *Client) Lookup(ctx context.Context, keys []model.DepKey) (map[model.DepKey]model.Risk, error) {
	out := map[model.DepKey]model.Risk{}
	var vreqs []any
	var projectOnly []model.DepKey
	seen := map[model.DepKey]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if k.System == "" {
			projectOnly = append(projectOnly, k)
			continue
		}
		out[k] = model.Risk{System: k.System}
		vreqs = append(vreqs, map[string]any{"versionKey": versionKey(k)})
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var verr, ferr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		verr = batch(ctx, c, "/versionbatch", vreqs, func(p *versionPage) string {
			mu.Lock()
			defer mu.Unlock()
			for _, x := range p.Responses {
				k := model.DepKey(x.Request.VersionKey)
				if x.Version == nil {
					continue
				}
				r := out[k]
				r.PublishedAt, r.Deprecated = x.Version.PublishedAt, x.Version.IsDeprecated
				for _, a := range x.Version.AdvisoryKeys {
					r.Advisories = append(r.Advisories, a.ID)
				}
				for _, rp := range x.Version.RelatedProjects {
					if rp.RelationType == "SOURCE_REPO" && r.SourceRepo == "" {
						r.SourceRepo = rp.ProjectKey.ID
					}
				}
				out[k] = r
			}
			return p.NextPageToken
		})
	}()
	go func() {
		defer wg.Done()
		ferr = batch(ctx, c, "/findingsbatch", vreqs, func(p *findingsPage) string {
			mu.Lock()
			defer mu.Unlock()
			for _, x := range p.Responses {
				k := model.DepKey(x.Request.VersionKey)
				r := out[k]
				var fs []finding
				if x.Findings.RequestedVersion != nil {
					fs = x.Findings.RequestedVersion.Findings
				}
				r.Findings = findingTypes(r.Findings, fs, x.Findings.PackageFindings)
				out[k] = r
			}
			return p.NextPageToken
		})
	}()
	wg.Wait()
	if err := errors.Join(verr, ferr); err != nil {
		return out, err
	}

	byProject := map[string][]model.DepKey{}
	for k, r := range out {
		if r.SourceRepo != "" {
			byProject[r.SourceRepo] = append(byProject[r.SourceRepo], k)
		}
	}
	for _, k := range projectOnly {
		byProject[k.Name] = append(byProject[k.Name], k)
	}
	ids := make([]string, 0, len(byProject))
	for id := range byProject {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	preqs := make([]any, len(ids))
	for i, id := range ids {
		preqs[i] = map[string]any{"projectKey": projectKey{ID: id}}
	}
	err := batch(ctx, c, "/projectbatch", preqs, func(p *projectPage) string {
		for _, x := range p.Responses {
			if x.Project == nil {
				continue
			}
			for _, k := range byProject[x.Request.ProjectKey.ID] {
				r := out[k]
				r.SourceRepo, r.Stars = x.Request.ProjectKey.ID, x.Project.StarsCount
				if x.Project.Scorecard != nil {
					r.Scorecard = x.Project.Scorecard.OverallScore
				}
				out[k] = r
			}
		}
		return p.NextPageToken
	})
	return out, err
}

// findingTypes appends the finding types not already in have, skipping
// REMEDIATION (it only says a newer version exists).
func findingTypes(have []string, lists ...[]finding) []string {
	for _, l := range lists {
		for _, f := range l {
			if f.Type == "REMEDIATION" || slices.Contains(have, f.Type) {
				continue
			}
			have = append(have, f.Type)
		}
	}
	return have
}

// batch posts reqs to path in chunks of maxBatch, following page tokens;
// each consumes a page and returns its next-page token.
func batch[P any](ctx context.Context, c *Client, path string, reqs []any, each func(*P) string) error {
	for start := 0; start < len(reqs); start += maxBatch {
		body := map[string]any{"requests": reqs[start:min(start+maxBatch, len(reqs))]}
		for {
			var page P
			if err := c.post(ctx, path, body, &page); err != nil {
				return err
			}
			next := each(&page)
			if next == "" {
				break
			}
			body["pageToken"] = next
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("deps.dev %s: %w", path, err)
		}
		if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("deps.dev %s: %w", path, err)
			}
			return nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retry || attempt >= maxRetries {
			return fmt.Errorf("deps.dev %s: %s", path, resp.Status)
		}
		if err := c.sleep(ctx, time.Duration(500<<attempt)*time.Millisecond); err != nil {
			return err
		}
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
