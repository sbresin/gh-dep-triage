package cli

import "github.com/sbresin/gh-dep-triage/internal/safe"

// sanitize drops terminal control characters from untrusted text.
func sanitize(s string) string { return safe.Text(s) }
