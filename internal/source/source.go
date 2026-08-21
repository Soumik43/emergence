// Package source is stage 1 of the pipeline: turn a seed input into candidate startups.
//
// Sources are deliberately deterministic and LLM-free. Stage 1's job is to produce a
// defensible list of real companies with real dated signals; judgement belongs in stage 2.
// Keeping the boundary there means sourcing can be re-run for pennies and diffed by eye.
package source

import (
	"context"

	"github.com/Soumik43/emergence/internal/model"
)

// Seed is the pipeline's input: what the partner typed.
type Seed struct {
	// Query is a topic ("AI agents for SMBs") or a feed identifier ("yc-w25"),
	// interpreted by the source.
	Query string
	// Limit caps returned candidates. The brief asks for 10–20.
	Limit int
	// MinSignal is the minimum traction score a hit needs to count as a candidate.
	// For Hacker News this is points. Zero means "use the source's default".
	MinSignal int
}

// Source fetches candidates for a seed. One implementation per site.
//
// The interface exists for the second source (YC), not for an imagined twelfth —
// see docs/decisions/001-two-sources-not-twelve.md.
type Source interface {
	// Name is the stable identifier written into Candidate.Source.Name.
	Name() string
	// Fetch returns candidates for the seed, most promising first. A source that
	// finds nothing returns an empty slice and no error; only transport and decode
	// failures are errors.
	Fetch(ctx context.Context, seed Seed) ([]model.Candidate, error)
}
