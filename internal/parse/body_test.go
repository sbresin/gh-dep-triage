package parse

import (
	"strings"
	"testing"
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
