package parse

import "testing"

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

func TestReleaseNotesNone(t *testing.T) {
	if got := ReleaseNotes("just a body"); got != "" {
		t.Errorf("got %q", got)
	}
}
