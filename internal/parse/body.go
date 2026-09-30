package parse

import (
	"regexp"
	"strings"
)

var (
	dependabotNotesRe = regexp.MustCompile(`(?is)<details>\s*<summary>\s*(?:release notes|changelog)\s*</summary>(.*?)</details>`)
	renovateNotesRe   = regexp.MustCompile(`(?im)^###\s+release notes\s*$`)
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
	if end := strings.Index(strings.ToLower(rest), "### configuration"); end >= 0 {
		rest = rest[:end]
	}
	rest = strings.TrimSpace(rest)
	return strings.TrimSpace(strings.TrimSuffix(rest, "---"))
}
