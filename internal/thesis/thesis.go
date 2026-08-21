// Package thesis is the single source of truth for what this fund invests in.
//
// The prose version lives in docs/THESIS.md and must be kept in step with the
// Criteria table below — the doc explains the reasoning, this file is what actually
// runs. The analysis prompt is generated from these definitions (see Brief), so the
// model and the scorer can never disagree about what a criterion means.
package thesis

// Statement is the thesis in one sentence. It is injected into the analysis prompt
// and printed in every memo header, so a reader always knows what the score is
// relative to.
const Statement = "Execution-layer AI that completes a recurring, measurable back-office " +
	"workflow for SMBs (10–500 employees), built by founders who have lived that workflow, " +
	"where every completed execution makes the next one better."

// Criterion is one scored dimension of the thesis.
type Criterion struct {
	Key    string
	Label  string
	Weight int // weights across all criteria must sum to 100; enforced by TestWeightsSumTo100
	// Zero and Five anchor the 0–5 scale for the model. Vague anchors are the main
	// cause of grade drift, so these are written as observable states, not adjectives.
	Zero string
	Five string
}

// Criteria is the rubric. Order is the order criteria appear in memos.
//
// Weights encode a claim: founder proximity to the workflow is the least fixable
// attribute at seed, so it carries the most weight; traction carries the least because
// at seed it mostly measures age. See docs/THESIS.md for the argument.
var Criteria = []Criterion{
	{
		Key:    "founder_workflow_proximity",
		Label:  "Founder ↔ workflow proximity",
		Weight: 25,
		Zero:   "No discoverable connection between any founder and the workflow being automated.",
		Five:   "A founder personally performed this job, or owned this function as an operator, and says so specifically.",
	},
	{
		Key:    "execution_depth",
		Label:  "Execution vs. suggestion",
		Weight: 20,
		Zero:   "Suggests, drafts, or summarises. A human still performs the action.",
		Five:   "Takes the terminal action end-to-end, with an explicit position on what happens when it gets one wrong.",
	},
	{
		Key:    "data_loop",
		Label:  "Compounding data loop",
		Weight: 20,
		Zero:   "Stateless wrapper over a foundation model. Nothing accumulates from use.",
		Five:   "Each execution yields proprietary outcome or correction data that demonstrably improves later executions.",
	},
	{
		Key:    "technical_depth",
		Label:  "Technical depth",
		Weight: 15,
		Zero:   "No evidence of engineering beyond prompt-plus-integration glue.",
		Five:   "Clear non-trivial systems work: evaluation harness, deterministic fallbacks, or hard integration depth.",
	},
	{
		Key:    "wedge_and_why_now",
		Label:  "Wedge & why now",
		Weight: 10,
		Zero:   "Positioning too broad to falsify in 12 months (\"AI for business\").",
		Five:   "One workflow, one buyer, one metric — plus a concrete reason this only became buildable recently.",
	},
	{
		Key:    "traction_signal",
		Label:  "Traction quality",
		Weight: 10,
		Zero:   "No traction signal, or vanity metrics only.",
		Five:   "Named paying customers, disclosed revenue, or retention past the novelty window.",
	},
}

// Bands map a weighted total to a call. Boundaries are inclusive lower bounds,
// checked in descending order.
var Bands = []struct {
	Min  int
	Call string
}{
	{70, "Take a meeting"},
	{45, "Watch"},
	{0, "Pass"},
}

// NoEvidenceGrade is the grade assigned when nothing could be found for a criterion.
//
// It is 1 rather than the midpoint on purpose: a seed fund's default is no, and for
// most of these criteria a founder who qualifies says so on their own landing page, so
// silence is weak evidence of absence. The cost — a quiet team gets under-scored — is
// surfaced through Score.Confidence rather than hidden.
const NoEvidenceGrade = 1

// MaxGrade is the top of the per-criterion scale.
const MaxGrade = 5

// ProvisionalBelow is the confidence level under which a call is labelled
// "on current evidence" in the memo instead of being stated flatly.
const ProvisionalBelow = 0.5

// ByKey returns the criterion with the given key.
func ByKey(key string) (Criterion, bool) {
	for _, c := range Criteria {
		if c.Key == key {
			return c, true
		}
	}
	return Criterion{}, false
}
