// Package safe makes untrusted text safe to print to a terminal.
package safe

import "strings"

// Text drops terminal control characters (C0 except \n and \t, DEL and C1)
// so escape sequences in untrusted text cannot reach the terminal.
func Text(s string) string {
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

// Line is Text collapsed onto one line, for single-line UI elements.
func Line(s string) string {
	return strings.Join(strings.Fields(Text(s)), " ")
}
