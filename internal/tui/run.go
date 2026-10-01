package tui

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// Run starts the TUI and returns the exit code (130 after ctrl+c, SIGINT,
// SIGTERM or a cancelled ctx, else 0) and one summary line per job.
//
// Bubble Tea's own signal handler is off: SIGINT/SIGTERM become signalMsg,
// so running jobs finish instead of being abandoned. This handler stays
// registered for the whole run, so a second SIGINT still reaches the TUI
// after cli.Execute has stopped its NotifyContext. ctx is watched (not
// passed to tea.WithContext, whose cancellation kills the program at once).
func Run(ctx context.Context, snap *model.Snapshot, deps Deps, in io.Reader, out io.Writer) (int, []string, error) {
	p := tea.NewProgram(New(ctx, snap, deps), tea.WithoutSignalHandler(), tea.WithInput(in), tea.WithOutput(out))
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	stop := forward(ctx, sigs, p.Send)
	defer stop()
	final, err := p.Run()
	fm, _ := final.(Model)
	switch {
	case err != nil:
		return 1, fm.Summary(), err
	case fm.Interrupted():
		return 130, fm.Summary(), nil
	}
	return 0, fm.Summary(), nil
}
