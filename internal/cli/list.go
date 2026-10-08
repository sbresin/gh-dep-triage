package cli

import (
	"encoding/json"
	"io"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/plan"
	"github.com/sbresin/gh-dep-triage/internal/triage"
	"github.com/spf13/cobra"
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
	var statuses, bumps, tiers []string
	var asPlan bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List dependency PRs with blockers and suggested actions",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output{command: "list"}
			if err := a.prepare(cmd); err != nil {
				return a.emit(out, err)
			}
			if len(args) > 0 {
				return a.emit(out, &usageError{msg: "list takes no arguments"})
			}
			byScope, err := scopeFilter(scope)
			if err != nil {
				return a.emit(out, err)
			}
			byStatus, err := oneOf(statuses, validStatuses, "--status must be ready, blocked or merging", func(p *model.PR) string { return string(p.Status) })
			if err != nil {
				return a.emit(out, err)
			}
			byBump, err := oneOf(bumps, validBumps, "--bump must be patch, minor, major or unknown", func(p *model.PR) string { return p.Bump })
			if err != nil {
				return a.emit(out, err)
			}
			byTier, err := oneOf(tiers, validTiers, "--tier must be auto, review or manual", func(p *model.PR) string { return p.Tier })
			if err != nil {
				return a.emit(out, err)
			}
			if asPlan && a.opts.json {
				return a.emit(out, &usageError{msg: "--plan prints a plan for apply; drop --json"})
			}
			keep := func(p *model.PR) bool { return byScope(p) && byStatus(p) && byBump(p) && byTier(p) }
			_, snap, err := a.loadSnapshot(cmd.Context())
			if err != nil {
				return a.emit(out, err)
			}
			a.annotate(snap.PRs())
			groups := filterGroups(snap.Groups, keep)
			out.viewer, out.warnings = snap.Viewer, snap.Warnings
			out.data = listData{Groups: groups, Counts: countStatuses(groups)}
			out.human = func(w io.Writer) { writeListTable(w, groups, a.now()) }
			if asPlan {
				out.human = func(w io.Writer) { writeMergePlan(w, groups) }
			}
			return a.emit(out, nil)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "all", "review | approved | all")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "only these statuses: ready,blocked,merging")
	cmd.Flags().StringSliceVar(&bumps, "bump", nil, "only these bump types: patch,minor,major,unknown")
	cmd.Flags().StringSliceVar(&tiers, "tier", nil, "only these tiers: auto,review,manual")
	cmd.Flags().BoolVar(&asPlan, "plan", false, "print a merge plan (with headOids) for apply --plan - instead of the table")
	return cmd
}

// writeMergePlan prints one merge item per PR, pinned to its head commit.
func writeMergePlan(w io.Writer, groups []*model.Group) {
	items := []plan.Item{}
	for _, g := range groups {
		for _, pr := range g.PRs {
			items = append(items, plan.Item{Action: model.ActionMerge, Ref: pr.Ref, HeadOid: pr.HeadOid})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(items)
}

var (
	validStatuses = map[string]bool{string(model.StatusReady): true, string(model.StatusBlocked): true, string(model.StatusMerging): true}
	validBumps    = map[string]bool{model.BumpPatch: true, model.BumpMinor: true, model.BumpMajor: true, model.BumpUnknown: true}
	validTiers    = map[string]bool{triage.TierAuto: true, triage.TierReview: true, triage.TierManual: true}
)

// oneOf keeps PRs whose field is one of vals (every PR when vals is empty).
func oneOf(vals []string, valid map[string]bool, msg string, field func(*model.PR) string) (func(*model.PR) bool, error) {
	set := map[string]bool{}
	for _, v := range vals {
		if !valid[v] {
			return nil, &usageError{msg: msg}
		}
		set[v] = true
	}
	return func(p *model.PR) bool { return len(set) == 0 || set[field(p)] }, nil
}
