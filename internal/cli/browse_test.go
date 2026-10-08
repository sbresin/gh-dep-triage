package cli

import (
	"io"
	"testing"

	cliBrowser "github.com/cli/browser"
)

// go-gh falls back to github.com/cli/browser when no browser is configured,
// and that package writes the launcher's output to its own globals
// (os.Stdout/os.Stderr by default), which would corrupt the TUI.
func TestQuietBrowseSilencesFallbackLauncher(t *testing.T) {
	oldOut, oldErr := cliBrowser.Stdout, cliBrowser.Stderr
	t.Cleanup(func() { cliBrowser.Stdout, cliBrowser.Stderr = oldOut, oldErr })
	if quietBrowse() == nil {
		t.Fatal("quietBrowse returned nil")
	}
	if cliBrowser.Stdout != io.Discard || cliBrowser.Stderr != io.Discard {
		t.Errorf("fallback launcher output not discarded: stdout=%v stderr=%v", cliBrowser.Stdout, cliBrowser.Stderr)
	}
}
