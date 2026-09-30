package triage

import (
	"sort"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var bumpRank = map[string]int{model.BumpMajor: 0, model.BumpMinor: 1, model.BumpPatch: 2, model.BumpUnknown: 3}

// InferBumps fills unknown bump types when all known PRs for the same
// (package, target) agree on one type.
func InferBumps(prs []*model.PR) {
	type key struct{ pkg, target string }
	known := map[key]map[string]bool{}
	for _, pr := range prs {
		if pr.TargetVersion == "" || pr.Bump == model.BumpUnknown {
			continue
		}
		k := key{pr.PackageKey, pr.TargetVersion}
		if known[k] == nil {
			known[k] = map[string]bool{}
		}
		known[k][pr.Bump] = true
	}
	for _, pr := range prs {
		if pr.Bump != model.BumpUnknown || pr.TargetVersion == "" {
			continue
		}
		if set := known[key{pr.PackageKey, pr.TargetVersion}]; len(set) == 1 {
			for b := range set {
				pr.Bump = b
			}
		}
	}
}

func slug(pkg string) string {
	return strings.Join(strings.Fields(strings.ToLower(pkg)), "-")
}

// BaseGroupID is the group ID without the ~bump disambiguation suffix.
func BaseGroupID(packageKey, target string) string {
	return "group:" + slug(packageKey) + "@" + target
}

func GroupPRs(prs []*model.PR) []*model.Group {
	type key struct{ pkg, target, bump string }
	byKey := map[key]*model.Group{}
	groups := []*model.Group{}
	for _, pr := range prs {
		k := key{pr.PackageKey, pr.TargetVersion, pr.Bump}
		g := byKey[k]
		if g == nil {
			g = &model.Group{PackageKey: pr.PackageKey, TargetVersion: pr.TargetVersion, Bump: pr.Bump}
			byKey[k] = g
			groups = append(groups, g)
		}
		g.PRs = append(g.PRs, pr)
	}
	for _, g := range groups {
		sort.Slice(g.PRs, func(i, j int) bool { return lessRef(g.PRs[i], g.PRs[j]) })
		g.Package = g.PRs[0].Package
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.PackageKey != b.PackageKey {
			return a.PackageKey < b.PackageKey
		}
		if a.TargetVersion != b.TargetVersion {
			return a.TargetVersion < b.TargetVersion
		}
		if bumpRank[a.Bump] != bumpRank[b.Bump] {
			return bumpRank[a.Bump] < bumpRank[b.Bump]
		}
		return lessRef(a.PRs[0], b.PRs[0])
	})
	assignIDs(groups)
	return groups
}

func lessRef(a, b *model.PR) bool {
	if a.Repo != b.Repo {
		return a.Repo < b.Repo
	}
	return a.Number < b.Number
}

func assignIDs(groups []*model.Group) {
	count := map[string]int{}
	for _, g := range groups {
		if g.TargetVersion != "" {
			count[BaseGroupID(g.PackageKey, g.TargetVersion)]++
		}
	}
	for _, g := range groups {
		if g.TargetVersion == "" {
			continue
		}
		base := BaseGroupID(g.PackageKey, g.TargetVersion)
		g.ID = base
		if count[base] > 1 {
			g.ID = base + "~" + g.Bump
		}
		for _, pr := range g.PRs {
			pr.GroupID = g.ID
		}
	}
}
