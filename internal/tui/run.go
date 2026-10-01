package tui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Run starts the TUI and returns the exit code (130 if the user quit with
// ctrl+c, else 0) and the results of the last execution, if any.
func Run(ctx context.Context, snap *model.Snapshot, deps Deps, in io.Reader, out io.Writer) (int, []model.Result, error) {
	final, err := tea.NewProgram(New(ctx, snap, deps), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return 1, nil, err
	}
	fm, ok := final.(Model)
	if !ok {
		return 0, nil, nil
	}
	if fm.Interrupted() {
		return 130, fm.Results(), nil
	}
	return 0, fm.Results(), nil
}
