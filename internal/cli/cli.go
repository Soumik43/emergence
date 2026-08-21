// Package cli wires the three stages into commands.
//
// Each stage is its own subcommand over the same run directory, so any stage can be
// re-run alone: `source` to refresh candidates, `analyze` to grade them, `memo` to
// re-render. `run` is the one-command path a partner uses.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Soumik43/emergence/internal/thesis"
)

// options are shared by every subcommand.
type options struct {
	query     string
	runsDir   string
	limit     int
	minSignal int
	force     bool
	// concurrency bounds parallel analyses. Each one makes several web searches, so
	// this is about being a reasonable API client, not about saturating a CPU.
	concurrency int
	// only restricts analysis to one candidate ID, for debugging a single memo
	// without paying for the other nineteen.
	only string
	// noLocalFetch forces every page read to the model's server-side fetcher, to
	// isolate whether a bad analysis came from thin local extraction or the model.
	noLocalFetch bool
	// noScreen skips the relevance screen, restoring a fully deterministic and free
	// stage 1 at the cost of analysing off-segment candidates.
	noScreen bool
}

// Execute runs the CLI.
func Execute(version string) int {
	var opts options

	root := &cobra.Command{
		Use:   "emergence",
		Short: "Triage startups against a stated investment thesis",
		Long: "emergence sources startups, analyses them against a stated thesis, and writes\n" +
			"one memo per company ending in Pass / Watch / Take a meeting.\n\n" +
			"Thesis: " + thesis.Statement,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&opts.query, "query", "q", "",
		"topic to source against, e.g. \"AI agents for SMBs\" (required)")
	root.PersistentFlags().StringVar(&opts.runsDir, "runs", "runs",
		"directory holding run output")
	root.PersistentFlags().IntVar(&opts.limit, "limit", 15,
		"maximum candidates to carry forward")
	root.PersistentFlags().IntVar(&opts.minSignal, "min-signal", 0,
		"minimum traction signal to accept a candidate (HN points); 0 uses the source default")
	root.PersistentFlags().BoolVar(&opts.force, "force", false,
		"redo work already cached in the run directory")
	root.PersistentFlags().BoolVar(&opts.noScreen, "no-screen", false,
		"skip the relevance screen (keeps stage 1 free and deterministic)")

	root.AddCommand(
		newRunCmd(&opts),
		newSourceCmd(&opts),
		newAnalyzeCmd(&opts),
		newMemoCmd(&opts),
		newThesisCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// requireQuery validates the one flag every stage needs.
func requireQuery(opts *options) error {
	if opts.query == "" {
		return fmt.Errorf("--query is required, e.g. --query \"AI agents for SMBs\"")
	}
	return nil
}
