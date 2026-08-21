package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Soumik43/emergence/internal/analyze"
	"github.com/Soumik43/emergence/internal/fetch"
	"github.com/Soumik43/emergence/internal/llm"
	"github.com/Soumik43/emergence/internal/memo"
	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/run"
	"github.com/Soumik43/emergence/internal/screen"
	"github.com/Soumik43/emergence/internal/source"
	"github.com/Soumik43/emergence/internal/thesis"
)

const sourceName = "hackernews"

func newRunCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Source, analyse, and write memos (the one-command path)",
		Long: "Runs all three stages against one topic. Safe to re-run: each stage skips work\n" +
			"already on disk unless --force, so an interrupted run resumes where it stopped\n" +
			"rather than re-paying for candidates it already analysed.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireQuery(opts); err != nil {
				return err
			}
			r, err := openRun(opts)
			if err != nil {
				return err
			}
			if err := doSource(cmd.Context(), r, opts); err != nil {
				return err
			}
			if err := doAnalyze(cmd.Context(), r, opts); err != nil {
				return err
			}
			return doMemo(r, opts)
		},
	}
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", 3,
		"candidates to analyse in parallel")
	cmd.Flags().StringVar(&opts.only, "only", "",
		"analyse just this candidate ID")
	cmd.Flags().BoolVar(&opts.noLocalFetch, "no-local-fetch", false,
		"skip the local fetcher and read every page server-side")
	return cmd
}

func newSourceCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "source",
		Short: "Stage 1: collect candidates from Hacker News",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireQuery(opts); err != nil {
				return err
			}
			r, err := openRun(opts)
			if err != nil {
				return err
			}
			return doSource(cmd.Context(), r, opts)
		},
	}
}

func newAnalyzeCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Stage 2: research and grade each candidate (costs tokens)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireQuery(opts); err != nil {
				return err
			}
			r, err := openRun(opts)
			if err != nil {
				return err
			}
			return doAnalyze(cmd.Context(), r, opts)
		},
	}
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", 3,
		"candidates to analyse in parallel")
	cmd.Flags().StringVar(&opts.only, "only", "",
		"analyse just this candidate ID")
	cmd.Flags().BoolVar(&opts.noLocalFetch, "no-local-fetch", false,
		"skip the local fetcher and read every page server-side")
	return cmd
}

func newMemoCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "memo",
		Short: "Stage 3: render memos and the run index (free, no model calls)",
		Long: "Re-renders every memo from the analyses already on disk. Costs nothing, so the\n" +
			"memo format can be iterated on without re-analysing anything.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireQuery(opts); err != nil {
				return err
			}
			r, err := openRun(opts)
			if err != nil {
				return err
			}
			return doMemo(r, opts)
		},
	}
}

func newThesisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "thesis",
		Short: "Print the thesis and scoring rubric everything is graded against",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s\n\n", thesis.Statement)
			fmt.Fprintf(out, "%-32s %s\n", "CRITERION", "WEIGHT")
			for _, c := range thesis.Criteria {
				fmt.Fprintf(out, "%-32s %5d   %s\n", c.Key, c.Weight, c.Label)
			}
			fmt.Fprintln(out)
			for _, b := range thesis.Bands {
				fmt.Fprintf(out, "%3d+  %s\n", b.Min, b.Call)
			}
			fmt.Fprintf(out, "\nUngradeable criteria score %d/%d, not the midpoint. See docs/THESIS.md.\n",
				thesis.NoEvidenceGrade, thesis.MaxGrade)
			return nil
		},
	}
}

func openRun(opts *options) (*run.Run, error) {
	return run.Open(opts.runsDir, opts.query, sourceName, opts.limit, thesis.Statement)
}

// doSource runs stage 1.
func doSource(ctx context.Context, r *run.Run, opts *options) error {
	if !opts.force {
		if existing, err := r.LoadCandidates(); err == nil && len(existing) > 0 {
			fmt.Printf("stage 1  %d candidates already sourced (--force to redo)\n", len(existing))
			return nil
		}
	}

	fmt.Printf("stage 1  searching Hacker News for %q\n", opts.query)

	cands, err := source.NewHackerNews().Fetch(ctx, source.Seed{
		Query: opts.query,
		// Source above the limit so the relevance screen has room to drop
		// off-segment candidates without leaving the run short.
		Limit:     opts.limit * 2,
		MinSignal: opts.minSignal,
	})
	if err != nil {
		return fmt.Errorf("sourcing: %w", err)
	}
	if len(cands) == 0 {
		return fmt.Errorf("sourcing found no candidates for %q — try a broader topic or a lower --min-signal", opts.query)
	}
	fmt.Printf("stage 1  %d raw candidates\n", len(cands))

	if !opts.noScreen {
		client := llm.New()
		if !client.HasCredentials() {
			return llm.ErrNoCredentials
		}
		absTrace, _ := r.TracePath("screen")
		kept, dropped, err := screen.New(client).Screen(ctx, cands, absTrace)
		if err != nil {
			// The screen fails soft: it returns everything on error, so a broken
			// screen costs money in stage 2 rather than emptying the pipeline.
			fmt.Printf("stage 1  screen unavailable, keeping all candidates: %v\n", err)
		}
		for _, d := range dropped {
			fmt.Printf("stage 1  screened out  %-24s %s\n", d.ID, d.Reason)
			r.Manifest.Screened = append(r.Manifest.Screened, run.ScreenedOut{
				ID: d.ID, Name: d.Name, Reason: d.Reason,
			})
		}
		cands = kept
	}

	if len(cands) > opts.limit {
		cands = cands[:opts.limit]
	}

	if err := r.SaveCandidates(cands); err != nil {
		return err
	}
	fmt.Printf("stage 1  %d candidates → %s\n", len(cands), r.Dir)
	return r.SaveManifest()
}

// doAnalyze runs stage 2, the only stage that costs money.
//
// Per-candidate caching is what makes that affordable to iterate on: an interrupted run,
// a crash, or a single bad memo can all be fixed without re-analysing the rest.
func doAnalyze(ctx context.Context, r *run.Run, opts *options) error {
	cands, err := r.LoadCandidates()
	if err != nil {
		return fmt.Errorf("no candidates on disk — run `emergence source` first: %w", err)
	}

	client := llm.New()
	if !client.HasCredentials() {
		return llm.ErrNoCredentials
	}

	analyzer := analyze.New(client, fetch.New())
	analyzer.SkipLocalFetch = opts.noLocalFetch

	// Decide the work list before starting, so the log line is honest about how much
	// of the run is cached.
	var todo []model.Candidate
	var cached int
	for _, c := range cands {
		if opts.only != "" && c.ID != opts.only {
			continue
		}
		if !opts.force {
			if _, found, err := r.LoadAnalysis(c.ID); err == nil && found {
				cached++
				continue
			}
		}
		todo = append(todo, c)
	}

	fmt.Printf("stage 2  %d to analyse, %d cached\n", len(todo), cached)
	if len(todo) == 0 {
		return nil
	}

	limit := opts.concurrency
	if limit < 1 {
		limit = 1
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, limit)
		done int
	)

	for _, c := range todo {
		wg.Add(1)
		go func(c model.Candidate) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			absTrace, relTrace := r.TracePath(c.ID)
			start := time.Now()
			an, err := analyzer.Analyze(ctx, c, absTrace)

			mu.Lock()
			defer mu.Unlock()
			done++

			if err != nil {
				// One failed candidate must not sink the run. The failure is
				// recorded and surfaced in the index rather than leaving a silent
				// gap in the output.
				fmt.Printf("stage 2  [%d/%d] %-24s FAILED: %v\n", done, len(todo), c.ID, err)
				r.RecordFailure(c.ID, err)
				return
			}

			an.Meta.TraceFile = relTrace
			if err := r.SaveAnalysis(an); err != nil {
				fmt.Printf("stage 2  [%d/%d] %-24s could not save: %v\n", done, len(todo), c.ID, err)
				r.RecordFailure(c.ID, err)
				return
			}
			r.AddCost(an.Meta)

			fmt.Printf("stage 2  [%d/%d] %-24s %3d/100 %-15s %d searches, %s\n",
				done, len(todo), c.ID, an.Score.Total, an.Score.Call,
				an.Meta.WebSearches, time.Since(start).Round(time.Second))
		}(c)
	}
	wg.Wait()

	r.Manifest.Analysed = countAnalyses(r)
	if err := r.SaveManifest(); err != nil {
		return err
	}

	// A run where everything failed is a failed run, and should exit non-zero rather
	// than reporting success and writing an empty index.
	if r.Manifest.Analysed == 0 {
		return errors.New("every candidate failed to analyse; see the errors above")
	}
	return nil
}

// doMemo runs stage 3.
func doMemo(r *run.Run, opts *options) error {
	analyses, err := r.LoadAllAnalyses()
	if err != nil {
		return fmt.Errorf("no analyses on disk — run `emergence analyze` first: %w", err)
	}
	if len(analyses) == 0 {
		return errors.New("no analyses to render")
	}

	cands, err := r.LoadCandidates()
	if err != nil {
		return err
	}
	byID := make(map[string]model.Candidate, len(cands))
	for _, c := range cands {
		byID[c.ID] = c
	}

	for _, a := range analyses {
		markdown, err := memo.Render(a, byID[a.CandidateID])
		if err != nil {
			return err
		}
		if err := r.SaveMemo(a.CandidateID, markdown); err != nil {
			return err
		}
	}

	r.Manifest.Analysed = len(analyses)
	if err := r.SaveManifest(); err != nil {
		return err
	}
	if err := r.WriteIndex(analyses); err != nil {
		return err
	}

	fmt.Printf("stage 3  %d memos → %s/memos/\n", len(analyses), r.Dir)
	fmt.Printf("\n%s\n", summarise(analyses))
	fmt.Printf("Start here: %s/index.md\n", r.Dir)
	return nil
}

// summarise prints the shortlist, since that is the actual output of a triage run.
func summarise(analyses []model.Analysis) string {
	var counts = map[model.Call]int{}
	for _, a := range analyses {
		counts[a.Score.Call]++
	}
	out := fmt.Sprintf("%d take a meeting · %d watch · %d pass\n",
		counts[model.CallMeeting], counts[model.CallWatch], counts[model.CallPass])
	for _, a := range analyses {
		if a.Score.Call != model.CallMeeting {
			continue
		}
		out += fmt.Sprintf("  → %-24s %3d/100  %s\n", a.Name, a.Score.Total, a.Website)
	}
	return out
}

func countAnalyses(r *run.Run) int {
	analyses, err := r.LoadAllAnalyses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not count analyses: %v\n", err)
		return 0
	}
	return len(analyses)
}
