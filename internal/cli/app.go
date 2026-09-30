// Package cli implements the gh dep-triage command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/loader"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// version is overridden at build time via -ldflags.
var version = "dev"

const (
	ExitOK      = 0
	ExitError   = 1
	ExitPartial = 2
	ExitDenied  = 3
)

type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

type globalOpts struct {
	limit   int
	workers int
	team    string
	json    bool
}

type app struct {
	stdout    io.Writer
	stderr    io.Writer
	opts      globalOpts
	newClient func() (github.Client, error)
	now       func() time.Time
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{
		stdout: os.Stdout,
		stderr: os.Stderr,
		now:    time.Now,
		newClient: func() (github.Client, error) {
			c, err := github.New()
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}
	return a.execute(ctx, os.Args[1:])
}

func (a *app) loadSnapshot(ctx context.Context) (github.Client, *model.Snapshot, error) {
	c, err := a.newClient()
	if err != nil {
		return nil, nil, err
	}
	snap, err := loader.Load(ctx, c, loader.Options{
		Limit: a.opts.limit, Workers: a.opts.workers, Team: a.opts.team, Now: a.now,
		Progress: func(format string, args ...any) { fmt.Fprintf(a.stderr, format+"\n", args...) },
	})
	return c, snap, err
}

func (a *app) execute(ctx context.Context, args []string) int {
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	cmd, err := root.ExecuteContextC(ctx)
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil && jsonRequested(args) {
		a.opts.json = true
		name := ""
		if cmd != nil && cmd != root {
			name = cmd.Name()
		}
		if errors.As(a.emit(output{command: name}, &usageError{msg: err.Error()}), &ee) {
			return ee.code
		}
		return ExitError
	}
	if err != nil {
		fmt.Fprintln(a.stderr, "error:", err)
		return ExitError
	}
	return ExitOK
}

// jsonRequested reports whether --json appears in args; flag parsing may stop
// on an earlier error before it is parsed.
func jsonRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dep-triage",
		Short:         "Triage and merge Dependabot/Renovate pull requests",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Annotations:   map[string]string{cobra.CommandDisplayNameAnnotation: "gh dep-triage"},
	}
	f := root.PersistentFlags()
	f.IntVar(&a.opts.limit, "limit", 200, "maximum results per search")
	f.IntVar(&a.opts.workers, "workers", 4, "parallel GraphQL requests while loading")
	f.StringVar(&a.opts.team, "team", "", "use review requests for a team (team or org/team) instead of you")
	f.BoolVar(&a.opts.json, "json", false, "print a JSON envelope on stdout")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{msg: err.Error()} })
	root.AddCommand(a.listCmd())
	root.AddCommand(a.showCmd())
	return root
}
