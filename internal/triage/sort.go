package triage

import (
	"sort"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var checkRank = map[string]int{"failed": 0, "pending": 1, "ok": 2}

func checkSeverity(c model.CheckSummary) string {
	switch {
	case c.Failed > 0:
		return "failed"
	case c.Pending > 0:
		return "pending"
	default:
		return "ok"
	}
}

// SortPRs returns prs sorted for the given TUI sort mode without modifying
// the input slice.
func SortPRs(prs []*model.PR, mode string) []*model.PR {
	out := append([]*model.PR(nil), prs...)
	byRef := func(a, b *model.PR) bool {
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Number < b.Number
	}
	var less func(a, b *model.PR) bool
	switch mode {
	case "severity":
		less = func(a, b *model.PR) bool {
			if bumpRank[a.Bump] != bumpRank[b.Bump] {
				return bumpRank[a.Bump] < bumpRank[b.Bump]
			}
			if a.PackageKey != b.PackageKey {
				return a.PackageKey < b.PackageKey
			}
			return byRef(a, b)
		}
	case "checks":
		less = func(a, b *model.PR) bool {
			ca, cb := checkRank[checkSeverity(a.Checks)], checkRank[checkSeverity(b.Checks)]
			if ca != cb {
				return ca < cb
			}
			if bumpRank[a.Bump] != bumpRank[b.Bump] {
				return bumpRank[a.Bump] < bumpRank[b.Bump]
			}
			if a.PackageKey != b.PackageKey {
				return a.PackageKey < b.PackageKey
			}
			return byRef(a, b)
		}
	case "repo":
		less = byRef
	default:
		less = func(a, b *model.PR) bool {
			if a.PackageKey != b.PackageKey {
				return a.PackageKey < b.PackageKey
			}
			if a.TargetVersion != b.TargetVersion {
				return a.TargetVersion < b.TargetVersion
			}
			if bumpRank[a.Bump] != bumpRank[b.Bump] {
				return bumpRank[a.Bump] < bumpRank[b.Bump]
			}
			return byRef(a, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}
