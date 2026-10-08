package parse

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReleaseNotesDependabot(t *testing.T) {
	body := "Bumps lodash.\n<details>\n<summary>Release notes</summary>\n<p>v4.17.21 fixes</p>\n</details>\n" +
		"<details>\n<summary>Changelog</summary>\n<p>changes</p>\n</details>\n" +
		"<details>\n<summary>Commits</summary>\n<p>abc</p>\n</details>"
	want := "<p>v4.17.21 fixes</p>\n\n<p>changes</p>"
	if got := ReleaseNotes(body); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReleaseNotesRenovate(t *testing.T) {
	body := "| Package | Change |\n\n---\n\n### Release Notes\n\n<details>\n<summary>facebook/react</summary>\n\n### v19.0.1\n- fix\n</details>\n\n---\n\n### Configuration\n\n📅 Schedule"
	want := "<details>\n<summary>facebook/react</summary>\n\n### v19.0.1\n- fix\n</details>"
	if got := ReleaseNotes(body); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReleaseNotesRenovateMultibyteBeforeConfiguration(t *testing.T) {
	notes := strings.Repeat("Ⱥ", 20)
	body := "### Release Notes\n\n" + notes + "\n\n---\n\n### Configuration\n\n📅 Schedule"
	if got := ReleaseNotes(body); got != notes {
		t.Errorf("got %q, want %q", got, notes)
	}
}

func TestReleaseNotesRenovateWithoutConfiguration(t *testing.T) {
	body := "### Release Notes\n\n<details>\n<summary>facebook/react</summary>\n- fix\n</details>\n\n"
	want := "<details>\n<summary>facebook/react</summary>\n- fix\n</details>"
	if got := ReleaseNotes(body); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReleaseNotesNone(t *testing.T) {
	if got := ReleaseNotes("just a body"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestRenovateBadge(t *testing.T) {
	const mend = "https://developer.mend.io/api/mc/badges/"
	tests := []struct {
		name string
		body string
		want Badge
		ok   bool
	}{
		{"confidence wins over age", "![age](" + mend + "age/pypi/django/6.1.1?slim=true) | ![confidence](" + mend + "confidence/pypi/django/6.0.8/6.1.1?slim=true)",
			Badge{"pypi", "django", "6.0.8", "6.1.1"}, true},
		{"age only", "![age](" + mend + "age/npm/lodash/4.17.21?slim=true)", Badge{"npm", "lodash", "", "4.17.21"}, true},
		{"encoded scoped name", "![c](" + mend + "confidence/npm/%40types%2Fnode/20.1.0/22.0.0?slim=true)", Badge{"npm", "@types/node", "20.1.0", "22.0.0"}, true},
		{"unencoded slashes", "![c](" + mend + "confidence/go/github.com/spf13/cobra/v1.8.0/v1.9.1?slim=true)", Badge{"go", "github.com/spf13/cobra", "v1.8.0", "v1.9.1"}, true},
		{"no badge", "Bumps lodash.", Badge{}, false},
	}
	for _, tt := range tests {
		got, ok := RenovateBadge(tt.body)
		if ok != tt.ok || !cmp.Equal(got, tt.want) {
			t.Errorf("%s: got %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBadgeMatches(t *testing.T) {
	b := Badge{To: "6.1.1"}
	if !BadgeMatches(b, "6.1.1") || !BadgeMatches(b, "v6.1.1") || BadgeMatches(b, "6.2.0") || BadgeMatches(b, "") {
		t.Error("BadgeMatches must compare targets, ignoring a leading v, and reject an empty target")
	}
}
