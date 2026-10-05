package parse

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	dependabotNotesRe = regexp.MustCompile(`(?is)<details>\s*<summary>\s*(?:release notes|changelog)\s*</summary>(.*?)</details>`)
	renovateNotesRe   = regexp.MustCompile(`(?im)^###\s+release notes\s*$`)
	renovateConfigRe  = regexp.MustCompile(`(?im)^###\s+configuration\b`)
	mendBadgeRe       = regexp.MustCompile(`developer\.mend\.io/api/mc/badges/(age|confidence)/([A-Za-z0-9_.-]+)/([^?)\s"]+)`)
)

// ReleaseNotes extracts release notes from a Dependabot or Renovate PR body.
func ReleaseNotes(body string) string {
	if ms := dependabotNotesRe.FindAllStringSubmatch(body, -1); len(ms) > 0 {
		parts := make([]string, 0, len(ms))
		for _, m := range ms {
			parts = append(parts, strings.TrimSpace(m[1]))
		}
		return strings.Join(parts, "\n\n")
	}
	loc := renovateNotesRe.FindStringIndex(body)
	if loc == nil {
		return ""
	}
	rest := body[loc[1]:]
	if end := renovateConfigRe.FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	rest = strings.TrimSpace(rest)
	return strings.TrimSpace(strings.TrimSuffix(rest, "---"))
}

// Badge is what a Renovate Merge Confidence badge URL encodes. From is empty
// when only the age badge is present.
type Badge struct{ Datasource, Package, From, To string }

// RenovateBadge reads the first confidence badge (falling back to the first
// age badge) in a Renovate PR body.
func RenovateBadge(body string) (Badge, bool) {
	var age Badge
	found := false
	for _, m := range mendBadgeRe.FindAllStringSubmatch(body, -1) {
		segs := strings.Split(m[3], "/")
		n := len(segs)
		switch {
		case m[1] == "confidence" && n >= 3:
			return newBadge(m[2], segs[:n-2], segs[n-2], segs[n-1]), true
		case m[1] == "age" && n >= 2 && !found:
			age, found = newBadge(m[2], segs[:n-1], "", segs[n-1]), true
		}
	}
	return age, found
}

// BadgeMatches reports whether the badge describes the update to target.
func BadgeMatches(b Badge, target string) bool {
	return target != "" && strings.TrimPrefix(b.To, "v") == strings.TrimPrefix(target, "v")
}

func newBadge(datasource string, pkg []string, from, to string) Badge {
	return Badge{Datasource: datasource, Package: unescape(strings.Join(pkg, "/")), From: unescape(from), To: unescape(to)}
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}
