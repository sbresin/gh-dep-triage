package cli

import "strings"

// sanitize drops terminal control characters (C0 except \n and \t, DEL and
// C1) from untrusted text so escape sequences cannot reach the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		}
		return r
	}, s)
}
