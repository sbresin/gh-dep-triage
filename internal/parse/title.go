// Package parse extracts package and version data from bot PR titles and bodies.
package parse

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

var (
	dependabotRe        = regexp.MustCompile(`(?i)\bbump (.+?) from (\S+) to (\S+)`)
	renovateTerraformRe = regexp.MustCompile(`(?i)\bupdate terraform ([a-z0-9._/-]+) to v?(\S+)`)
	renovateGenericRe   = regexp.MustCompile(`(?i)\bupdate (?:dependency |package |github action )?(.+?) to v?(\S+)`)
	dependabotDirRe     = regexp.MustCompile(`^\s+in\s+(/\S*)`)
	trailingActionRe    = regexp.MustCompile(`(?i)\s+action$`)
	leadingNumbersRe    = regexp.MustCompile(`^\d+(?:\.\d+)*`)
	requirementRe       = regexp.MustCompile(`(?i)\bupdate (.+?) requirement from (.+?) to (.+?)(?:\s+in\s+(/\S*))?\s*$`)
	versionTokenRe      = regexp.MustCompile(`\d+(?:\.\d+)*`)
)

type Title struct {
	Package    string
	PackageKey string
	Source     string
	Target     string
	Bump       string
	Directory  string
}

func ParseTitle(title string) Title {
	if loc := dependabotRe.FindStringSubmatchIndex(title); loc != nil {
		pkg, src, dst := cleanToken(title[loc[2]:loc[3]]), cleanToken(title[loc[4]:loc[5]]), cleanToken(title[loc[6]:loc[7]])
		dir := ""
		if d := dependabotDirRe.FindStringSubmatch(title[loc[1]:]); d != nil {
			dir = cleanToken(d[1])
		}
		return Title{Package: pkg, PackageKey: NormalizePackage(pkg), Source: src, Target: dst, Bump: ClassifyBump(src, dst), Directory: dir}
	}
	if m := requirementRe.FindStringSubmatch(title); m != nil {
		pkg, src, dst := cleanToken(m[1]), cleanToken(m[2]), cleanToken(m[3])
		return Title{Package: pkg, PackageKey: NormalizePackage(pkg), Source: src, Target: dst,
			Bump: ClassifyBump(HighestVersion(src), HighestVersion(dst)), Directory: cleanToken(m[4])}
	}
	if m := renovateTerraformRe.FindStringSubmatch(title); m != nil {
		pkg := cleanToken(m[1])
		if !strings.Contains(pkg, "/") {
			pkg = "hashicorp/" + pkg
		}
		target := cleanToken(m[2])
		return Title{Package: pkg, PackageKey: NormalizePackage(pkg), Target: target, Bump: renovateBump(target)}
	}
	if m := renovateGenericRe.FindStringSubmatch(title); m != nil {
		pkg := trailingActionRe.ReplaceAllString(cleanToken(m[1]), "")
		target := cleanToken(m[2])
		return Title{Package: pkg, PackageKey: NormalizePackage(pkg), Target: target, Bump: renovateBump(target)}
	}
	fallback := strings.TrimSpace(title)
	return Title{Package: fallback, PackageKey: NormalizePackage(fallback), Bump: model.BumpUnknown}
}

// renovateBump classifies a Renovate target: Renovate's default title uses
// "to v<major>" only for major updates.
func renovateBump(target string) string {
	if len(versionSegments(target)) == 1 {
		return model.BumpMajor
	}
	return model.BumpUnknown
}

func NormalizePackage(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func cleanToken(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`'\"")
	return strings.TrimRight(s, ",.;:")
}

func versionSegments(v string) []int {
	m := leadingNumbersRe.FindString(strings.TrimLeft(cleanToken(v), "vV"))
	if m == "" {
		return nil
	}
	parts := strings.Split(m, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

func pad(a []int, n int) []int {
	out := make([]int, n)
	copy(out, a)
	return out
}

func ClassifyBump(source, target string) string {
	s, t := versionSegments(source), versionSegments(target)
	if s == nil || t == nil {
		return model.BumpUnknown
	}
	size := max(3, len(s), len(t))
	s, t = pad(s, size), pad(t, size)
	for i := range size {
		if s[i] == t[i] {
			continue
		}
		switch i {
		case 0:
			return model.BumpMajor
		case 1:
			return model.BumpMinor
		default:
			return model.BumpPatch
		}
	}
	return model.BumpPatch
}

// Major returns the first numeric version segment; ok is false when v has no
// numeric prefix.
func Major(v string) (int, bool) {
	s := versionSegments(v)
	if s == nil {
		return 0, false
	}
	return s[0], true
}

// CompareVersions compares numeric version prefixes. ok is false when either
// side has no numeric prefix.
func CompareVersions(a, b string) (int, bool) {
	x, y := versionSegments(a), versionSegments(b)
	if x == nil || y == nil {
		return 0, false
	}
	size := max(len(x), len(y))
	x, y = pad(x, size), pad(y, size)
	for i := range size {
		if x[i] > y[i] {
			return 1, true
		}
		if x[i] < y[i] {
			return -1, true
		}
	}
	return 0, true
}

// HighestVersion returns the largest numeric version in a requirement range
// such as "<2.4,>=2.1" ("" if it has none).
func HighestVersion(r string) string {
	best := ""
	for _, v := range versionTokenRe.FindAllString(r, -1) {
		if c, ok := CompareVersions(v, best); best == "" || (ok && c > 0) {
			best = v
		}
	}
	return best
}
