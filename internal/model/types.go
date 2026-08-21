// Package model holds the data shapes passed between pipeline stages.
//
// Every stage reads the previous stage's JSON off disk and writes its own, so these
// structs are the pipeline's actual interface. Field names are the committed wire format:
// renaming one invalidates existing runs.
package model

import "time"

// Candidate is the output of stage 1 (sourcing). One startup, before any judgement
// has been applied to it.
type Candidate struct {
	ID       string   `json:"id"` // slug, stable across runs; used as the filename
	Name     string   `json:"name"`
	Website  string   `json:"website"`
	OneLiner string   `json:"one_liner"`
	Founders []string `json:"founders,omitempty"`

	// Signals is the freshness/traction evidence. At least one is required for a
	// candidate to survive sourcing — a startup with no signal is just a website.
	Signals []Signal `json:"signals"`

	Source       SourceRef `json:"source"`
	DiscoveredAt time.Time `json:"discovered_at"`
}

// Signal is one dated, sourced piece of freshness or traction evidence.
type Signal struct {
	Kind   SignalKind `json:"kind"`
	Detail string     `json:"detail"` // human-readable, e.g. "Show HN: 312 points, 140 comments"
	Date   time.Time  `json:"date"`
	URL    string     `json:"url"` // must be checkable by a reviewer
}

type SignalKind string

const (
	SignalHNLaunch     SignalKind = "hn_launch"     // a Show HN / Launch HN post
	SignalHNDiscussion SignalKind = "hn_discussion" // organic front-page discussion
	SignalYCBatch      SignalKind = "yc_batch"      // appears in a YC batch listing
)

// SourceRef records where a candidate came from, so any candidate can be traced back
// to the exact query that surfaced it.
type SourceRef struct {
	Name  string `json:"name"`  // "hackernews", "ycombinator"
	Query string `json:"query"` // the seed input that produced this hit
	URL   string `json:"url"`   // the source-side permalink
}

// Analysis is the output of stage 2. Everything here except Score is produced by the
// model; Score is computed in Go from Criteria (see docs/decisions/003-score-in-code.md).
type Analysis struct {
	CandidateID string    `json:"candidate_id"`
	Name        string    `json:"name"`
	Website     string    `json:"website"`
	AnalyzedAt  time.Time `json:"analyzed_at"`

	Team    Section `json:"team"`
	Product Section `json:"product"`
	Market  Section `json:"market"`
	Risks   []Risk  `json:"risks"`

	// Criteria holds one graded entry per thesis criterion, in thesis order.
	Criteria []CriterionGrade `json:"criteria"`

	// AnalystNote is free text that deliberately bypasses the rubric, for an
	// observation the six criteria cannot express. Rendered verbatim in the memo.
	AnalystNote string `json:"analyst_note,omitempty"`

	Score Score `json:"score"`
	Meta  Meta  `json:"meta"`
}

// Section is one narrative block of the analysis with the evidence behind it.
type Section struct {
	Summary  string     `json:"summary"`
	Evidence []Evidence `json:"evidence"`
}

// Evidence ties a specific claim to a specific URL. A claim with no URL is not
// evidence, and the memo renderer marks it as unsourced rather than dropping it.
type Evidence struct {
	Claim string `json:"claim"`
	URL   string `json:"url"`
}

// Risk is something that could kill the company, graded by how fatal it is.
type Risk struct {
	Risk     string   `json:"risk"`
	Severity Severity `json:"severity"`
	// Falsifier is what would resolve this risk one way or the other. These feed
	// the memo's "what would change my mind" section.
	Falsifier string `json:"falsifier,omitempty"`
}

type Severity string

const (
	SeverityKill  Severity = "kill"  // if true, the company does not work
	SeverityMajor Severity = "major" // materially changes the outcome
	SeverityMinor Severity = "minor" // priced in, worth noting
)

// CriterionGrade is the model's 0–5 grade for one thesis criterion.
type CriterionGrade struct {
	Key           string     `json:"key"`   // matches thesis.Criterion.Key
	Grade         int        `json:"grade"` // 0–5; clamped on load
	Justification string     `json:"justification"`
	Evidence      []Evidence `json:"evidence,omitempty"`
	// NoEvidence is set when the model could not find anything to grade against.
	// This is a first-class outcome, not an error: it drives the confidence penalty.
	NoEvidence bool `json:"no_evidence"`
}

// Score is computed by internal/thesis. Nothing here is model output.
type Score struct {
	Total      int               `json:"total"`      // 0–100, weighted
	Call       Call              `json:"call"`       // derived from Total
	Confidence float64           `json:"confidence"` // 0–1, from evidence coverage
	Breakdown  []ScoredCriterion `json:"breakdown"`
	// Provisional is true when confidence is low enough that the call should be
	// read as "on current evidence" rather than as a settled judgement.
	Provisional bool `json:"provisional"`
}

// ScoredCriterion is one row of the score's arithmetic, kept so a reader can see
// exactly how the total was reached.
type ScoredCriterion struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Grade   int     `json:"grade"`   // 0–5 as graded
	Weight  int     `json:"weight"`  // from the thesis
	Points  float64 `json:"points"`  // grade/5 * weight
	Imputed bool    `json:"imputed"` // graded without evidence
}

type Call string

const (
	CallMeeting Call = "Take a meeting"
	CallWatch   Call = "Watch"
	CallPass    Call = "Pass"
)

// Meta records how an analysis was produced, so a reviewer can find the exact
// LLM exchange behind any claim in a memo.
type Meta struct {
	Model        string `json:"model"`
	TraceFile    string `json:"trace_file"` // path, relative to the run dir
	WebSearches  int    `json:"web_searches"`
	WebFetches   int    `json:"web_fetches"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	DurationMS   int64  `json:"duration_ms"`
}
