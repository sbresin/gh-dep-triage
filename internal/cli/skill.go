package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sbresin/gh-dep-triage/skill"
	"github.com/spf13/cobra"
)

func (a *app) skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the agent skill (SKILL.md)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := io.WriteString(a.stdout, skill.Content)
			return err
		},
	}
	var dir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Write SKILL.md to <dir>/gh-dep-triage/SKILL.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := output{command: "skill install"}
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return a.emit(out, err)
				}
				dir = filepath.Join(home, ".agents", "skills")
			}
			path := filepath.Join(dir, "gh-dep-triage", "SKILL.md")
			err := os.MkdirAll(filepath.Dir(path), 0o755)
			if err == nil {
				err = os.WriteFile(path, []byte(skill.Content), 0o644)
			}
			if err != nil {
				return a.emit(out, err)
			}
			out.data = map[string]string{"path": path}
			out.human = func(w io.Writer) { fmt.Fprintln(w, "installed", path) }
			return a.emit(out, nil)
		},
	}
	install.Flags().StringVar(&dir, "dir", "", "skills directory (default ~/.agents/skills)")
	cmd.AddCommand(install)
	return cmd
}
