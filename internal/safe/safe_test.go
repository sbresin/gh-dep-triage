package safe

import "testing"

func TestText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"keeps\nnewlines\tand tabs", "keeps\nnewlines\tand tabs"},
		{"\x1b]52;c;SGVsbG8=\x07after", "]52;c;SGVsbG8=after"},
		{"\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"over\rwrite", "overwrite"},
		{"nul\x00bs\x08del\x7f", "nulbsdel"},
		{"c1\u009b31m\u0085x", "c131mx"},
		{"unicode ✓ é", "unicode ✓ é"},
	}
	for _, tt := range tests {
		if got := Text(tt.in); got != tt.want {
			t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Bump  a\nfrom 1\tto 2  ", "Bump a from 1 to 2"},
		{"\x1b[31mred\x1b[0m title", "[31mred[0m title"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Line(tt.in); got != tt.want {
			t.Errorf("Line(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
