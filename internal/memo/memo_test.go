package memo

import (
	"strings"
	"testing"
	"time"

	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
)

func candidate() model.Candidate {
	return model.Candidate{
		ID:       "ledgerly",
		Name:     "Ledgerly",
		Website:  "https://ledgerly.com",
		OneLiner: "bookkeeping autopilot for restaurants",
		Signals: []model.Signal{{
			Kind:   model.SignalHNLaunch,
			Detail: "Show HN launch — 140 points, 70 comments",
			Date:   time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
			URL:    "https://news.ycombinator.com/item?id=1",
		}},
		Source: model.SourceRef{Name: "hackernews", Query: "AI agents for SMBs"},
	}
}

// graded builds an analysis whose criteria are graded per the map, with evidence for
// every key present in it and nothing for the rest.
func graded(grades map[string]int) model.Analysis {
	a := model.Analysis{
		CandidateID: "ledgerly",
		Name:        "Ledgerly Inc.",
		Website:     "https://ledgerly.com",
		AnalyzedAt:  time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
		Product:     model.Section{Summary: "Files restaurant tax returns without a bookkeeper. It categorises every transaction, then submits the return unattended."},
		Team:        model.Section{Summary: "Two founders, one a former restaurant-group CFO."},
		Market:      model.Section{Summary: "About 700k US restaurants. Competes with Bench and Intuit."},
		Meta: model.Meta{
			Model:       "claude-opus-5",
			TraceFile:   "trace/ledgerly.json",
			PageFetch:   "ok",
			WebSearches: 7,
		},
	}
	for _, c := range thesis.Criteria {
		g, present := grades[c.Key]
		entry := model.CriterionGrade{
			Key:           c.Key,
			Grade:         g,
			Justification: "graded because of a specific reason",
		}
		if present {
			entry.Evidence = []model.Evidence{{Claim: "a checkable fact", URL: "https://ledgerly.com/about"}}
		} else {
			entry.NoEvidence = true
		}
		a.Criteria = append(a.Criteria, entry)
	}
	a.Score = thesis.Score(a.Criteria)
	return a
}

func allGrades(grade int) map[string]int {
	m := map[string]int{}
	for _, c := range thesis.Criteria {
		m[c.Key] = grade
	}
	return m
}

func render(t *testing.T, a model.Analysis) string {
	t.Helper()
	got, err := Render(a, candidate())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return got
}

// The sixty-second test: name, call, score and the reason have to be in the opening
// lines, not buried under a table.
func TestRenderLeadsWithTheCall(t *testing.T) {
	a := graded(allGrades(4))

	got := render(t, a)

	head := strings.SplitN(got, "---", 2)[0]
	for _, want := range []string{"Ledgerly Inc.", "Take a meeting", "80/100", "What would change my mind"} {
		if !strings.Contains(head, want) {
			t.Errorf("%q is not in the memo's opening section", want)
		}
	}
	if !strings.HasPrefix(got, "# Ledgerly Inc. — Take a meeting") {
		t.Errorf("memo does not open with the name and call:\n%s", firstLines(got, 3))
	}
	// The one-liner heads the memo, so it must be one sentence rather than the whole
	// product paragraph. The full paragraph belongs further down under Product, so
	// this assertion is scoped to the blockquote line only.
	quote := lineStartingWith(got, "> ")
	if quote == "" {
		t.Fatal("no one-line blockquote in the memo")
	}
	if strings.Contains(quote, "It categorises every transaction") {
		t.Errorf("the full product paragraph leaked into the one-line summary: %q", quote)
	}
	if !strings.Contains(got, "It categorises every transaction") {
		t.Error("the full product paragraph is missing from the Product section")
	}
}

func TestRenderShowsTheArithmetic(t *testing.T) {
	a := graded(allGrades(4))

	got := render(t, a)

	// Every criterion needs a visible row, or the score isn't auditable.
	for _, c := range thesis.Criteria {
		if !strings.Contains(got, c.Label) {
			t.Errorf("criterion %q missing from the memo", c.Label)
		}
	}
	if !strings.Contains(got, "| **Total** | | **100** | **80** |") {
		t.Error("the total row is missing or wrong; a reader can't check the sum")
	}
	if !strings.Contains(got, "[source](https://ledgerly.com/about)") {
		t.Error("evidence links missing — claims must be traceable")
	}
	if !strings.Contains(got, "trace/ledgerly.json") {
		t.Error("trace file not referenced; a reviewer can't find the exchange")
	}
}

// The output has to stay useful when the analysis is mostly gaps, since that is the
// common case on a thin candidate rather than an edge case.
func TestRenderOnMostlyMissingEvidence(t *testing.T) {
	// Only the lowest-weight criterion has evidence.
	a := graded(map[string]int{"traction_signal": 3})

	got := render(t, a)

	if !strings.Contains(got, "Pass") {
		t.Error("a near-evidence-free analysis should not read as anything but Pass")
	}
	if !strings.Contains(got, "provisional") {
		t.Error("low confidence is not flagged as provisional")
	}
	if !strings.Contains(got, "on current evidence") {
		t.Error("the call is stated flatly despite low confidence")
	}
	if !strings.Contains(got, "†") {
		t.Error("imputed criteria are not marked in the score table")
	}
	if !strings.Contains(got, "No evidence found") {
		t.Error("missing evidence is not explained per criterion")
	}
	// With no risks recorded, the falsifier list must fall back to the evidence gaps
	// that carry the most weight — not be empty.
	if !strings.Contains(strings.ToLower(got), "founder ↔ workflow proximity") {
		t.Error("highest-weight evidence gap is not surfaced in change-my-mind")
	}
}

func TestRenderChangeMyMind(t *testing.T) {
	t.Run("risk falsifiers come first, worst risk first", func(t *testing.T) {
		a := graded(allGrades(4))
		a.Risks = []model.Risk{
			{Risk: "minor thing", Severity: model.SeverityMinor, Falsifier: "Check the minor thing"},
			{Risk: "Intuit bundles this", Severity: model.SeverityKill, Falsifier: "Ask whether QBO ships auto-filing in 2026"},
			{Risk: "pricing untested", Severity: model.SeverityMajor, Falsifier: "Ask for the win rate at list price"},
		}
		a.Score = thesis.Score(a.Criteria)

		got := render(t, a)
		list := section(got, "**What would change my mind**", "---")

		kill := strings.Index(list, "QBO ships auto-filing")
		major := strings.Index(list, "win rate at list price")
		minor := strings.Index(list, "Check the minor thing")
		if kill < 0 || major < 0 {
			t.Fatalf("falsifiers missing from the list:\n%s", list)
		}
		if kill > major {
			t.Error("the fatal risk's falsifier is not listed first")
		}
		if minor >= 0 && minor < major {
			t.Error("a minor risk outranked a major one")
		}
		// The cap keeps the list actionable.
		if strings.Count(list, "\n1. ")+strings.Count(list, "\n2. ")+strings.Count(list, "\n3. ") > 3 {
			t.Error("more than three items in change-my-mind")
		}
		if strings.Contains(list, "4. ") {
			t.Errorf("list exceeded %d items", maxChangeMyMind)
		}
	})

	t.Run("a fatal risk is named in the rationale", func(t *testing.T) {
		a := graded(allGrades(4))
		a.Risks = []model.Risk{{
			Risk: "Intuit bundles this into QuickBooks", Severity: model.SeverityKill,
			Falsifier: "Ask about the QBO roadmap",
		}}
		a.Score = thesis.Score(a.Criteria)

		got := render(t, a)

		if !strings.Contains(got, "One risk is rated fatal") {
			t.Error("a kill-severity risk is not called out in the rationale")
		}
	})

	t.Run("no risks and full evidence still yields a non-empty list", func(t *testing.T) {
		got := render(t, graded(allGrades(5)))
		list := section(got, "**What would change my mind**", "---")

		if strings.TrimSpace(strings.TrimPrefix(list, "**What would change my mind**")) == "" {
			t.Error("change-my-mind is empty; the memo must always give a partner something to test")
		}
	})
}

func TestRenderMarksUnsourcedSectionClaims(t *testing.T) {
	a := graded(allGrades(3))
	a.Team.Evidence = []model.Evidence{
		{Claim: "Founder was CFO at a restaurant group", URL: "https://linkedin.com/in/x"},
		{Claim: "The team appears technical", URL: ""},
	}

	got := render(t, a)

	if !strings.Contains(got, "[source](https://linkedin.com/in/x)") {
		t.Error("sourced claim lost its link")
	}
	if !strings.Contains(got, "*unsourced*") {
		t.Error("an unsourced claim is presented with the same weight as a sourced one")
	}
}

func TestRenderCallBands(t *testing.T) {
	tests := []struct {
		grade int
		want  string
	}{
		{5, "Take a meeting"},
		{3, "Watch"},
		{1, "Pass"},
	}
	for _, tc := range tests {
		a := graded(allGrades(tc.grade))
		got := render(t, a)
		if !strings.Contains(got, "## The call: "+tc.want) {
			t.Errorf("grade %d (score %d) rendered call is not %q", tc.grade, a.Score.Total, tc.want)
		}
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a := graded(allGrades(4))
	a.Risks = []model.Risk{
		{Risk: "one", Severity: model.SeverityMajor, Falsifier: "check one"},
		{Risk: "two", Severity: model.SeverityKill, Falsifier: "check two"},
	}
	a.Score = thesis.Score(a.Criteria)

	first := render(t, a)
	for i := 0; i < 5; i++ {
		if got := render(t, a); got != first {
			t.Fatal("Render is not deterministic across calls; map iteration is leaking into output")
		}
	}
}

// An empty analysis is what a hard failure upstream leaves behind. It must still render
// something a person can read rather than panicking.
func TestRenderEmptyAnalysis(t *testing.T) {
	got, err := Render(model.Analysis{}, model.Candidate{})
	if err != nil {
		t.Fatalf("Render on an empty analysis: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("rendered nothing")
	}
	if !strings.Contains(got, "no information") {
		t.Error("an ungraded analysis should say its score is meaningless")
	}
}

func TestTidyBlankLines(t *testing.T) {
	got := tidyBlankLines("a\n\n\n\nb\n   \n\nc\n\n\n")
	want := "a\n\nb\n\nc\n"
	if got != want {
		t.Errorf("tidyBlankLines = %q, want %q", got, want)
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct{ in, want string }{
		{"One sentence. Second one.", "One sentence."},
		{"No terminator here", "No terminator here"},
		{"Ledgerly Inc. files returns for restaurants.", "Ledgerly Inc."},
		{"", ""},
	}
	for _, tc := range tests {
		if got := firstSentence(tc.in); got != tc.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// lineStartingWith returns the first line carrying the given prefix.
func lineStartingWith(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// firstLines is a test helper for readable failure output.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// section extracts the text between a start marker and the next end marker.
func section(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i:]
	if j := strings.Index(rest[len(start):], end); j >= 0 {
		return rest[:len(start)+j]
	}
	return rest
}
