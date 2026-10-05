package cli

import (
	"context"
	"fmt"

	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/tui"
	"github.com/spf13/cobra"
)

// tui loads a snapshot (with progress on stderr) and runs the interactive UI
// on top of a work queue, then prints one summary line per finished job.
func (a *app) tui(cmd *cobra.Command) error {
	out := output{}
	if err := a.prepare(cmd); err != nil {
		return a.emit(out, err)
	}
	client, snap, err := a.loadSnapshot(cmd.Context())
	if err != nil {
		return a.emit(out, err)
	}
	if len(snap.PRs()) == 0 {
		fmt.Fprintln(a.stderr, "No matching Dependabot or Renovate PRs found.")
		return nil
	}
	who := "user " + snap.Viewer
	if a.opts.team != "" {
		who = "team " + a.opts.team
	}
	rules := policy.Rules{Bots: a.cfg.Bots}
	// The TUI decides when the queue stops (Stop), so a Ctrl-C that cancels
	// cmd.Context() must not kill running jobs behind its back.
	q := executor.NewQueue(context.WithoutCancel(cmd.Context()), client,
		executor.Options{Viewer: snap.Viewer, Rules: rules, Sleep: a.sleep})
	deps := tui.Deps{
		Load: func(ctx context.Context) (*model.Snapshot, error) {
			_, s, err := a.loadSnapshotWith(ctx, false)
			return s, err
		},
		Queue:  q,
		Browse: a.browse,
		Rules:  rules,
		Who:    who,
	}
	code, summary, err := a.runTUI(cmd.Context(), snap, deps)
	if err != nil {
		return err
	}
	for _, line := range summary {
		fmt.Fprintln(a.stderr, line)
	}
	if code != ExitOK {
		return &exitError{code: code}
	}
	return nil
}
