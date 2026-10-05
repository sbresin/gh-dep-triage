package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/sbresin/gh-dep-triage/internal/model"
)

type resultCounts struct {
	Planned int `json:"planned"`
	Success int `json:"success"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
	Denied  int `json:"denied"`
	Total   int `json:"total"`
}

type resultsData struct {
	Results []model.Result `json:"results"`
	Counts  resultCounts   `json:"counts"`
}

func countResults(rs []model.Result) resultCounts {
	var c resultCounts
	for _, r := range rs {
		c.Total++
		switch r.Status {
		case model.ResultPlanned:
			c.Planned++
		case model.ResultSuccess:
			c.Success++
		case model.ResultSkipped:
			c.Skipped++
		case model.ResultFailed:
			c.Failed++
		case model.ResultDenied:
			c.Denied++
		}
	}
	return c
}

func exitCodeFor(rs []model.Result, dryRun bool) int {
	c := countResults(rs)
	switch {
	case c.Total > 0 && c.Denied == c.Total:
		return ExitDenied
	case c.Failed > 0 || anyCancelled(rs):
		return ExitPartial
	case c.Denied > 0 && !dryRun:
		return ExitPartial
	default:
		return ExitOK
	}
}

func anyCancelled(rs []model.Result) bool {
	for _, r := range rs {
		if r.Reason == model.ReasonCancelled {
			return true
		}
	}
	return false
}

// actionLabel is the action plus its arg, e.g. "close (superseded)".
func actionLabel(r model.Result) string {
	for _, v := range r.Args { // each action takes at most one arg
		return r.Action + " (" + sanitize(v) + ")"
	}
	return r.Action
}

func resultDetail(r model.Result) string {
	if r.Message != "" {
		return r.Message
	}
	return strings.Join(r.Steps, ", ")
}

func writeResults(w io.Writer, rs []model.Result, dryRun bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tACTION\tREF\tREASON\tDETAIL")
	for _, r := range rs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Status, actionLabel(r), sanitize(r.Ref), dash(r.Reason), sanitize(dash(resultDetail(r))))
	}
	tw.Flush()
	c := countResults(rs)
	fmt.Fprintf(w, "\n%d items: %d planned, %d succeeded, %d skipped, %d failed, %d denied\n",
		c.Total, c.Planned, c.Success, c.Skipped, c.Failed, c.Denied)
	if dryRun {
		fmt.Fprintln(w, "Dry run: nothing was changed. Re-run with --yes to execute.")
	}
}
