package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runApp(t *testing.T, a *app, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	a.stdout, a.stderr = &out, &errOut
	code := a.execute(context.Background(), args)
	return code, out.String(), errOut.String()
}

func TestVersionFlag(t *testing.T) {
	code, out, _ := runApp(t, &app{}, "--version")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "dev") {
		t.Errorf("stdout = %q, want version", out)
	}
}

func TestWorkersFlagDefault(t *testing.T) {
	f := (&app{}).rootCmd().PersistentFlags().Lookup("workers")
	if f == nil || f.DefValue != "16" {
		t.Errorf("--workers default = %v, want 16", f)
	}
}
