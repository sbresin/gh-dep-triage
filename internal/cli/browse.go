package cli

import (
	"io"

	cliBrowser "github.com/cli/browser"
	"github.com/cli/go-gh/v2/pkg/browser"
)

// quietBrowse returns a URL opener whose launcher output never reaches the
// terminal. go-gh honours the writers it is given only for a configured
// launcher; without one it falls back to github.com/cli/browser, which writes
// to that package's global Stdout/Stderr instead.
func quietBrowse() func(url string) error {
	cliBrowser.Stdout, cliBrowser.Stderr = io.Discard, io.Discard
	return browser.New("", io.Discard, io.Discard).Browse
}
