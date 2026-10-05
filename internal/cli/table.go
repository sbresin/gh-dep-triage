package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func updateLabel(pr *model.PR) string {
	switch {
	case pr.TargetVersion == "":
		return truncate(sanitize(pr.Title), 60)
	case pr.SourceVersion != "":
		return sanitize(fmt.Sprintf("%s %s -> %s", pr.Package, pr.SourceVersion, pr.TargetVersion))
	default:
		return sanitize(fmt.Sprintf("%s -> %s", pr.Package, pr.TargetVersion))
	}
}

func checksLabel(c model.CheckSummary) string {
	parts := []string{}
	if c.Failed > 0 {
		parts = append(parts, fmt.Sprintf("F%d", c.Failed))
	}
	if c.Pending > 0 {
		parts = append(parts, fmt.Sprintf("P%d", c.Pending))
	}
	if len(parts) > 0 {
		return strings.Join(parts, "/")
	}
	if c.Total > 0 {
		return "OK"
	}
	return "--"
}

func blockerCodes(pr *model.PR) string {
	codes := make([]string, len(pr.Blockers))
	for i, b := range pr.Blockers {
		codes[i] = b.Code
	}
	return dash(strings.Join(codes, ","))
}

func writeListTable(w io.Writer, groups []*model.Group) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tREF\tUPDATE\tBUMP\tCHECKS\tBLOCKERS\tPOLICY\tRISK\tGROUP")
	for _, g := range groups {
		for _, pr := range g.PRs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pr.Status, sanitize(pr.Ref), updateLabel(pr), pr.Bump, checksLabel(pr.Checks), blockerCodes(pr), dash(pr.MergeDenied),
				sanitize(riskLabel(pr.Risk)), sanitize(dash(pr.GroupID)))
		}
	}
	tw.Flush()
	c := countStatuses(groups)
	fmt.Fprintf(w, "\n%d PRs: %d ready, %d merging, %d blocked\n", c.Total, c.Ready, c.Merging, c.Blocked)
}

// riskLabel is the first deps.dev finding, else "<stars>★ <scorecard>".
func riskLabel(r *model.Risk) string {
	switch {
	case r == nil:
		return "-"
	case len(r.Findings) > 0:
		return r.Findings[0]
	case r.Stars == 0:
		return "-"
	}
	stars := strconv.Itoa(r.Stars)
	if r.Stars >= 1000 {
		stars = strconv.Itoa(r.Stars/1000) + "k"
	}
	if r.Scorecard > 0 {
		return fmt.Sprintf("%s★ %.1f", stars, r.Scorecard)
	}
	return stars + "★"
}
