package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sbresin/gh-dep-triage/internal/executor"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/policy"
	"github.com/sbresin/gh-dep-triage/internal/tui"
)

// tui loads a snapshot (with progress on stderr) and runs the interactive UI.
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
	deps := tui.Deps{
		Load: func(ctx context.Context) (*model.Snapshot, error) {
			_, s, err := a.loadSnapshotWith(ctx, false)
			return s, err
		},
		Execute: func(ctx context.Context, tasks []plan.Task, onResult func(model.Result)) []model.Result {
			return executor.Run(ctx, client, tasks, executor.Options{Viewer: snap.Viewer, Sleep: a.sleep, OnResult: onResult})
		},
		Browse: a.browse,
		Rules:  policy.Rules{Bots: a.cfg.Bots},
		Who:    who,
	}
	code, err := a.runTUI(cmd.Context(), snap, deps)
	if err != nil {
		return err
	}
	if code != ExitOK {
		return &exitError{code: code}
	}
	return nil
}
