package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/parse"
	"github.com/sbresin/gh-dep-triage/internal/triage"
	"github.com/spf13/cobra"
)

const logTailLines = 200

type jobLog struct {
	Check string `json:"check"`
	JobID int64  `json:"jobId,omitempty"`
	Log   string `json:"log,omitempty"`
	Error string `json:"error,omitempty"`
}

type showData struct {
	PR           *model.PR           `json:"pr"`
	Body         string              `json:"body"`
	ReleaseNotes string              `json:"releaseNotes"`
	CheckRuns    []model.Check       `json:"checkRuns"`
	Files        []model.ChangedFile `json:"files"`
	Logs         []jobLog            `json:"logs"`
	Checkout     string              `json:"checkout"`
}

func (a *app) showCmd() *cobra.Command {
	var withLogs bool
	cmd := &cobra.Command{
		Use:   "show <owner/repo#123>",
		Short: "Show one PR with body, release notes, files and optional failed-job logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output{command: "show"}
			if err := a.prepare(cmd); err != nil {
				return a.emit(out, err)
			}
			if len(args) != 1 {
				return a.emit(out, &usageError{msg: "show takes exactly one PR ref (owner/repo#123)"})
			}
			ref, err := triage.ParseRef(args[0])
			if err != nil {
				return a.emit(out, err)
			}
			if ref.Kind != triage.RefPR {
				return a.emit(out, &usageError{msg: "show takes a PR ref (owner/repo#123), not a group"})
			}
			client, snap, err := a.loadSnapshot(cmd.Context())
			if err != nil {
				return a.emit(out, err)
			}
			out.viewer, out.warnings = snap.Viewer, snap.Warnings
			pr := snap.Find(ref.PR)
			if pr == nil {
				return a.emit(out, &triage.RefError{Code: "not_found", Ref: args[0],
					Message: fmt.Sprintf("%s is not in your triage snapshot (no review requested and not approved by you)", args[0])})
			}
			a.markMergeDenied([]*model.PR{pr})
			files, err := client.PRFiles(cmd.Context(), ref.PR)
			if err != nil {
				return a.emit(out, fmt.Errorf("list changed files: %w", err))
			}
			if files == nil {
				files = []model.ChangedFile{}
			}
			data := showData{PR: pr, Body: pr.Body, ReleaseNotes: parse.ReleaseNotes(pr.Body), CheckRuns: pr.CheckRuns,
				Files: files, Logs: []jobLog{}, Checkout: triage.CheckoutHint(pr)}
			if withLogs {
				data.Logs = failedJobLogs(cmd.Context(), client, pr)
			}
			out.data = data
			out.human = func(w io.Writer) { writeShow(w, data) }
			return a.emit(out, nil)
		},
	}
	cmd.Flags().BoolVar(&withLogs, "logs", false, "include the last 200 lines of each failed GitHub Actions job log")
	return cmd
}

func failedJobLogs(ctx context.Context, c github.Client, pr *model.PR) []jobLog {
	logs := []jobLog{}
	for _, check := range pr.CheckRuns {
		if check.State != model.CheckStateFailed {
			continue
		}
		if check.Kind != model.CheckKindRun || check.JobID == 0 || !strings.Contains(check.URL, "/actions/runs/") {
			logs = append(logs, jobLog{Check: check.Name, Error: "not a GitHub Actions job; logs unavailable"})
			continue
		}
		entry := jobLog{Check: check.Name, JobID: check.JobID}
		if text, err := c.JobLog(ctx, pr.Repo, check.JobID); err != nil {
			entry.Error = err.Error()
		} else {
			entry.Log = tailLines(text, logTailLines)
		}
		logs = append(logs, entry)
	}
	return logs
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func writeShow(w io.Writer, d showData) {
	pr := d.PR
	fmt.Fprintf(w, "%s  %s  [%s]  %s\n", sanitize(pr.Ref), updateLabel(pr), pr.Bump, pr.Status)
	fmt.Fprintf(w, "URL:      %s\n", sanitize(pr.URL))
	fmt.Fprintf(w, "Group:    %s\n", sanitize(dash(pr.GroupID)))
	fmt.Fprintf(w, "Head:     %s\n", sanitize(pr.HeadOid))
	fmt.Fprintf(w, "Checks:   %s\n", checksLabel(pr.Checks))
	if pr.MergeDenied != "" {
		fmt.Fprintf(w, "Merge:    denied (%s)\n", pr.MergeDenied)
	}
	if r := pr.Risk; r != nil {
		fmt.Fprintf(w, "Risk:     %s  %s", sanitize(dash(r.Label())), sanitize(dash(r.SourceRepo)))
		if len(r.Findings) > 1 {
			fmt.Fprintf(w, "  findings: %s", sanitize(strings.Join(r.Findings, ", ")))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Checkout: %s\n", sanitize(d.Checkout))
	if len(pr.Blockers) > 0 {
		fmt.Fprintln(w, "\nBlockers:")
		for _, b := range pr.Blockers {
			fmt.Fprintf(w, "  - %s: %s\n", b.Code, sanitize(b.Detail))
			for _, act := range b.SuggestedActions {
				fmt.Fprintf(w, "      %s: %s\n", act.Action, sanitize(dash(act.Command)))
				if act.Checkout != "" {
					fmt.Fprintf(w, "      checkout: %s\n", sanitize(act.Checkout))
				}
			}
		}
	}
	if len(d.Files) > 0 {
		fmt.Fprintln(w, "\nFiles:")
		for _, f := range d.Files {
			fmt.Fprintf(w, "  %s (+%d -%d)\n", sanitize(f.Path), f.Additions, f.Deletions)
		}
	}
	if d.ReleaseNotes != "" {
		fmt.Fprintf(w, "\nRelease notes:\n%s\n", sanitize(d.ReleaseNotes))
	}
	for _, l := range d.Logs {
		fmt.Fprintf(w, "\nLog: %s\n", sanitize(l.Check))
		if l.Error != "" {
			fmt.Fprintf(w, "  (%s)\n", sanitize(l.Error))
		} else {
			fmt.Fprintln(w, sanitize(l.Log))
		}
	}
}
