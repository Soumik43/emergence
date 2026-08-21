// Command emergence triages startups against a stated investment thesis.
//
//	emergence run --query "AI agents for SMBs"
//
// See README.md for the pipeline's shape and docs/THESIS.md for what the scores mean.
package main

import (
	"os"

	"github.com/Soumik43/emergence/internal/cli"
)

// version is overridden at build time: -ldflags "-X main.version=$(git describe --tags)"
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
