package cli

import (
	"os"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/spf13/cobra"
)

var applyActions = map[string]bool{
	model.ActionApprove: true, model.ActionMerge: true, model.ActionRebase: true, model.ActionRecreate: true,
	model.ActionRerun: true, model.ActionRequestReview: true, model.ActionClose: true,
}

func (a *app) applyCmd() *cobra.Command {
	var mo mutateOpts
	var file string
	cmd := &cobra.Command{
		Use:   "apply --plan <file|->",
		Short: "Execute a JSON plan of action items (dry run unless --yes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output{command: "apply", dryRun: !mo.yes}
			if len(args) > 0 {
				return a.emit(out, &usageError{msg: "apply takes no arguments; pass the plan with --plan <file|->"})
			}
			if file == "" {
				return a.emit(out, &usageError{msg: "--plan <file|-> is required"})
			}
			r := a.stdin
			if file != "-" {
				f, err := os.Open(file)
				if err != nil {
					return a.emit(out, &usageError{msg: err.Error()})
				}
				defer f.Close()
				r = f
			}
			items, err := plan.Parse(r, applyActions)
			if err != nil {
				return a.emit(out, err)
			}
			return a.runPlan(cmd, "apply", items, mo)
		},
	}
	cmd.Flags().StringVar(&file, "plan", "", "plan file, or - for stdin")
	cmd.Flags().BoolVar(&mo.yes, "yes", false, "execute the plan (default is a dry run)")
	cmd.Flags().BoolVar(&mo.allowMajor, "allow-major", false, "allow major version bumps")
	return cmd
}
