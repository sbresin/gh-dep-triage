package cli

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

type counts struct {
	Ready   int `json:"ready"`
	Merging int `json:"merging"`
	Blocked int `json:"blocked"`
	Total   int `json:"total"`
}

type listData struct {
	Groups []*model.Group `json:"groups"`
	Counts counts         `json:"counts"`
}

func scopeFilter(scope string) (func(*model.PR) bool, error) {
	switch scope {
	case "all":
		return func(*model.PR) bool { return true }, nil
	case "review":
		return func(p *model.PR) bool { return p.RequestedForReview }, nil
	case "approved":
		return func(p *model.PR) bool { return p.ViewerApproved && !p.RequestedForReview }, nil
	default:
		return nil, &usageError{msg: "--scope must be review, approved or all"}
	}
}

func filterGroups(groups []*model.Group, keep func(*model.PR) bool) []*model.Group {
	out := []*model.Group{}
	for _, g := range groups {
		prs := []*model.PR{}
		for _, pr := range g.PRs {
			if keep(pr) {
				prs = append(prs, pr)
			}
		}
		if len(prs) > 0 {
			cp := *g
			cp.PRs = prs
			out = append(out, &cp)
		}
	}
	return out
}

func countStatuses(groups []*model.Group) counts {
	var c counts
	for _, g := range groups {
		for _, pr := range g.PRs {
			c.Total++
			switch pr.Status {
			case model.StatusReady:
				c.Ready++
			case model.StatusMerging:
				c.Merging++
			case model.StatusBlocked:
				c.Blocked++
			}
		}
	}
	return c
}

func (a *app) listCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List dependency PRs with blockers and suggested actions",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output{command: "list"}
			if len(args) > 0 {
				return a.emit(out, &usageError{msg: "list takes no arguments"})
			}
			keep, err := scopeFilter(scope)
			if err != nil {
				return a.emit(out, err)
			}
			_, snap, err := a.loadSnapshot(cmd.Context())
			if err != nil {
				return a.emit(out, err)
			}
			groups := filterGroups(snap.Groups, keep)
			out.viewer, out.warnings = snap.Viewer, snap.Warnings
			out.data = listData{Groups: groups, Counts: countStatuses(groups)}
			out.human = func(w io.Writer) { writeListTable(w, groups) }
			return a.emit(out, nil)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "all", "review | approved | all")
	return cmd
}
