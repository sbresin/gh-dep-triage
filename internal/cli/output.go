package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sbresin/gh-dep-triage/internal/github"
	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/triage"
)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

type output struct {
	command  string
	viewer   string
	data     any
	warnings []model.Problem
	human    func(io.Writer)
}

type envelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Command       string          `json:"command"`
	Viewer        string          `json:"viewer"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	DryRun        bool            `json:"dryRun"`
	Data          any             `json:"data"`
	Warnings      []model.Problem `json:"warnings"`
	Errors        []model.Problem `json:"errors"`
}

func toProblem(err error) model.Problem {
	var re *triage.RefError
	var ue *usageError
	switch {
	case errors.As(err, &re):
		return model.Problem{Code: re.Code, Message: re.Message, Ref: re.Ref, Candidates: re.Candidates}
	case errors.As(err, &ue):
		return model.Problem{Code: "invalid_argument", Message: ue.msg}
	case errors.Is(err, github.ErrNotAuthenticated):
		return model.Problem{Code: "not_authenticated", Message: err.Error()}
	default:
		return model.Problem{Code: "error", Message: err.Error()}
	}
}

// emit writes the command result (JSON envelope or human output) and returns
// an *exitError for non-zero exit codes.
func (a *app) emit(out output, err error) error {
	errs := []model.Problem{}
	if err != nil {
		errs = append(errs, toProblem(err))
	}
	warnings := out.warnings
	if warnings == nil {
		warnings = []model.Problem{}
	}
	if a.opts.json {
		data := out.data
		if data == nil {
			data = struct{}{}
		}
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		env := envelope{SchemaVersion: model.SchemaVersion, Command: out.command, Viewer: out.viewer,
			GeneratedAt: a.now().UTC(), Data: data, Warnings: warnings, Errors: errs}
		if encErr := enc.Encode(env); encErr != nil {
			return encErr
		}
	} else {
		for _, w := range warnings {
			if w.Ref != "" {
				fmt.Fprintf(a.stderr, "warning: %s: %s\n", sanitize(w.Ref), sanitize(w.Message))
			} else {
				fmt.Fprintf(a.stderr, "warning: %s\n", sanitize(w.Message))
			}
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "error: %s\n", sanitize(errs[0].Message))
			for _, c := range errs[0].Candidates {
				fmt.Fprintf(a.stderr, "  %s\n", sanitize(c))
			}
		} else if out.human != nil {
			out.human(a.stdout)
		}
	}
	if err != nil {
		return &exitError{code: ExitError}
	}
	return nil
}
