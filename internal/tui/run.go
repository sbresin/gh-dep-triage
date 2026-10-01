package tui

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Run starts the TUI and returns the exit code (130 if the user quit with
// ctrl+c, on SIGINT or when ctx is cancelled, else 0) and the results of the
// last execution, if any.
func Run(ctx context.Context, snap *model.Snapshot, deps Deps, in io.Reader, out io.Writer) (int, []model.Result, error) {
	final, err := tea.NewProgram(New(ctx, snap, deps), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	fm, _ := final.(Model)
	switch {
	case errors.Is(err, tea.ErrInterrupted), errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		return 130, fm.Results(), nil
	case err != nil:
		return 1, nil, err
	case fm.Interrupted():
		return 130, fm.Results(), nil
	}
	return 0, fm.Results(), nil
}
