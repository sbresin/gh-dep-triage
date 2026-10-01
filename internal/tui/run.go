package tui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Run starts the TUI and returns the exit code: 130 if the user quit with
// ctrl+c, else 0.
func Run(ctx context.Context, snap *model.Snapshot, deps Deps, in io.Reader, out io.Writer) (int, error) {
	final, err := tea.NewProgram(New(ctx, snap, deps), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return 1, err
	}
	if fm, ok := final.(Model); ok && fm.Interrupted() {
		return 130, nil
	}
	return 0, nil
}
