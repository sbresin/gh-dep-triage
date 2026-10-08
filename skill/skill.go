// Package skill embeds the agent skill shipped with gh dep-triage.
package skill

import _ "embed"

//go:embed SKILL.md
var Content string
