// Package cli implements the gh dep-triage command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
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
	stdout io.Writer
	stderr io.Writer
	opts   globalOpts
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	return a.execute(ctx, os.Args[1:])
}

func (a *app) execute(ctx context.Context, args []string) int {
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	err := root.ExecuteContext(ctx)
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil {
		fmt.Fprintln(a.stderr, "error:", err)
		return ExitError
	}
	return ExitOK
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
	return root
}
