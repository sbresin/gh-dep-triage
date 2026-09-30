package main

import (
	"os"

	"github.com/sbresin/gh-dep-triage/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
