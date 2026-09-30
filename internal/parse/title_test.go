package parse

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseTitle(t *testing.T) {
	tests := []struct {
		title string
		want  Title
	}{
		{"Bump lodash from 4.17.20 to 4.17.21", Title{"lodash", "lodash", "4.17.20", "4.17.21", "patch"}},
		{"build(deps): bump @types/node from 20.1.0 to 22.0.0", Title{"@types/node", "@types/node", "20.1.0", "22.0.0", "major"}},
		{"Bump github.com/spf13/cobra from 1.8.0 to 1.9.1", Title{"github.com/spf13/cobra", "github.com/spf13/cobra", "1.8.0", "1.9.1", "minor"}},
		{"Bump `actions/checkout` from v3 to v4.", Title{"actions/checkout", "actions/checkout", "v3", "v4", "major"}},
		{"Bump Django from 4.2 to 5.0", Title{"Django", "django", "4.2", "5.0", "major"}},
		{"chore(deps): update terraform aws to v5.1.0", Title{"hashicorp/aws", "hashicorp/aws", "", "5.1.0", "unknown"}},
		{"Update terraform integrations/github to v6.2.0", Title{"integrations/github", "integrations/github", "", "6.2.0", "unknown"}},
		{"fix(deps): update dependency react to v19.0.1", Title{"react", "react", "", "19.0.1", "unknown"}},
		{"chore(deps): update actions/setup-go action to v6", Title{"actions/setup-go", "actions/setup-go", "", "6", "unknown"}},
		{"Update github action actions/cache to v4.1.0", Title{"actions/cache", "actions/cache", "", "4.1.0", "unknown"}},
		{"chore(deps): update react monorepo to v19", Title{"react monorepo", "react monorepo", "", "19", "unknown"}},
		{"Update module github.com/foo/bar to v1.2.3", Title{"module github.com/foo/bar", "module github.com/foo/bar", "", "1.2.3", "unknown"}},
		{"  Weird   spacing title ", Title{"Weird   spacing title", "weird spacing title", "", "", "unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, ParseTitle(tt.title)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestClassifyBump(t *testing.T) {
	tests := []struct{ src, dst, want string }{
		{"1.2.3", "1.2.3", "patch"},
		{"1.2", "1.3", "minor"},
		{"1", "2", "major"},
		{"1.2.3.4", "1.2.3.5", "patch"},
		{"v1.0.0", "v1.1.0", "minor"},
		{"1.0.0-beta", "1.0.1", "patch"},
		{"abc", "1.0", "unknown"},
		{"", "1.0", "unknown"},
	}
	for _, tt := range tests {
		if got := ClassifyBump(tt.src, tt.dst); got != tt.want {
			t.Errorf("ClassifyBump(%q, %q) = %q, want %q", tt.src, tt.dst, got, tt.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b   string
		want   int
		wantOK bool
	}{
		{"4.17.21", "4.17.20", 1, true},
		{"1.0", "1.0.0", 0, true},
		{"v2", "10", -1, true},
		{"abc", "1", 0, false},
	}
	for _, tt := range tests {
		got, ok := CompareVersions(tt.a, tt.b)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("CompareVersions(%q, %q) = %d, %v", tt.a, tt.b, got, ok)
		}
	}
}
