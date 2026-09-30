// Package loader builds a triage snapshot from GitHub.
package loader

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

var DefaultBots = []string{"dependabot", "renovate"}

type Options struct {
	Limit    int
	Workers  int
	Team     string
	Bots     []string
	Now      func() time.Time
	Progress func(format string, args ...any)
}

func (o Options) withDefaults() Options {
	if o.Limit <= 0 {
		o.Limit = 200
	}
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if len(o.Bots) == 0 {
		o.Bots = DefaultBots
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Progress == nil {
		o.Progress = func(string, ...any) {}
	}
	return o
}

type candidate struct {
	ref                 model.PRRef
	requested, reviewed bool
}

func Load(ctx context.Context, c github.Client, o Options) (*model.Snapshot, error) {
	o = o.withDefaults()
	viewer, err := c.Viewer(ctx)
	if err != nil {
		return nil, fmt.Errorf("get viewer: %w", err)
	}

	o.Progress("Searching for dependency PRs…")
	requestedQ, reviewedQ := github.SearchQueries(o.Team, o.Bots)
	var requested, reviewed []github.SearchHit
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); requested, errA = c.SearchPRs(ctx, requestedQ, o.Limit) }()
	go func() { defer wg.Done(); reviewed, errB = c.SearchPRs(ctx, reviewedQ, o.Limit) }()
	wg.Wait()
	if err := errors.Join(errA, errB); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	cands := mergeCandidates(requested, reviewed, o.Bots)
	o.Progress("Fetching details for %d PRs…", len(cands))
	prs, warnings := fetchAll(ctx, c, viewer, cands, o.Workers)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	byRef := map[string]candidate{}
	for _, cd := range cands {
		byRef[cd.ref.String()] = cd
	}
	kept := []*model.PR{}
	for _, pr := range prs {
		cd := byRef[pr.Ref]
		pr.RequestedForReview, pr.SeenInReviewedSearch = cd.requested, cd.reviewed
		triage.Enrich(pr)
		if !strings.EqualFold(pr.State, "OPEN") || pr.IsDraft {
			continue
		}
		if !pr.RequestedForReview && !pr.ViewerApproved {
			continue
		}
		kept = append(kept, pr)
	}

	now := o.Now().UTC()
	groups := triage.Build(kept, now)
	if groups == nil {
		groups = []*model.Group{}
	}
	return &model.Snapshot{Viewer: viewer, GeneratedAt: now, Groups: groups, Warnings: warnings}, nil
}

func mergeCandidates(requested, reviewed []github.SearchHit, bots []string) []candidate {
	allowed := map[string]bool{}
	for _, b := range bots {
		allowed[strings.TrimSuffix(strings.ToLower(b), "[bot]")] = true
	}
	byRef := map[model.PRRef]*candidate{}
	add := func(hits []github.SearchHit, isRequested bool) {
		for _, h := range hits {
			if h.AuthorType != "Bot" || !allowed[strings.TrimSuffix(strings.ToLower(h.AuthorLogin), "[bot]")] {
				continue
			}
			ref := model.PRRef{Repo: h.Repo, Number: h.Number}
			cd := byRef[ref]
			if cd == nil {
				cd = &candidate{ref: ref}
				byRef[ref] = cd
			}
			if isRequested {
				cd.requested = true
			} else {
				cd.reviewed = true
			}
		}
	}
	add(requested, true)
	add(reviewed, false)

	out := make([]candidate, 0, len(byRef))
	for _, cd := range byRef {
		out = append(out, *cd)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ref.Repo != out[j].ref.Repo {
			return out[i].ref.Repo < out[j].ref.Repo
		}
		return out[i].ref.Number < out[j].ref.Number
	})
	return out
}

func fetchAll(ctx context.Context, c github.Client, viewer string, cands []candidate, workers int) ([]*model.PR, []model.Problem) {
	refs := make([]model.PRRef, len(cands))
	for i, cd := range cands {
		refs[i] = cd.ref
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		prs      []*model.PR
		warnings = []model.Problem{}
		sem      = make(chan struct{}, workers)
	)
dispatch:
	for start := 0; start < len(refs); start += github.BatchSize {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		batch := refs[start:min(start+github.BatchSize, len(refs))]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			got, warns, err := c.FetchPRs(ctx, viewer, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				for _, r := range batch {
					warnings = append(warnings, model.Problem{Code: "fetch_failed", Ref: r.String(), Message: err.Error()})
				}
				return
			}
			prs = append(prs, got...)
			warnings = append(warnings, warns...)
		}()
	}
	wg.Wait()
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Ref < warnings[j].Ref })
	return prs, warnings
}
