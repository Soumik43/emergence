package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Soumik43/emergence/internal/model"
)

func open(t *testing.T, root, query string) *Run {
	t.Helper()
	r, err := Open(root, query, "hackernews", 15, "test thesis")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

func analysis(id string, total int, call model.Call, conf float64) model.Analysis {
	return model.Analysis{
		CandidateID: id,
		Name:        strings.ToUpper(id[:1]) + id[1:],
		Product:     model.Section{Summary: "Does a thing. And another thing."},
		Score: model.Score{
			Total: total, Call: call, Confidence: conf,
			Breakdown: []model.ScoredCriterion{
				{Key: "founder_workflow_proximity", Label: "Founder ↔ workflow proximity", Grade: 4, Weight: 25},
				{Key: "data_loop", Label: "Compounding data loop", Grade: 1, Weight: 20, Imputed: true},
			},
		},
		Meta: model.Meta{Model: "claude-opus-5", InputTokens: 1000, OutputTokens: 500, WebSearches: 5, PageFetch: "ok"},
	}
}

// The run ID has to be deterministic, because that is what makes resuming the default
// rather than a flag someone has to remember.
func TestIDIsDeterministic(t *testing.T) {
	day := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)

	got := ID("AI agents for SMBs", day)
	if got != "ai-agents-for-smbs-2026-08-21" {
		t.Errorf("ID = %q", got)
	}
	if again := ID("AI agents for SMBs", day); again != got {
		t.Error("ID is not stable across calls")
	}
	// Punctuation and case must not produce a different directory for the same query.
	if other := ID("  ai   AGENTS for SMBs!  ", day); other != got {
		t.Errorf("ID(%q) = %q, want the same slug", "  ai   AGENTS for SMBs!  ", other)
	}
	if empty := ID("", day); empty != "run-2026-08-21" {
		t.Errorf("ID(\"\") = %q, want a usable fallback", empty)
	}
}

func TestOpenResumesAnExistingRun(t *testing.T) {
	root := t.TempDir()

	first := open(t, root, "AI agents for SMBs")
	if first.Resumed() {
		t.Error("Resumed() = true on a fresh run")
	}
	if err := first.SaveCandidates([]model.Candidate{{ID: "ledgerly", Name: "Ledgerly"}}); err != nil {
		t.Fatal(err)
	}
	first.AddCost(model.Meta{InputTokens: 500, OutputTokens: 200, WebSearches: 3, PageFetch: "ok"})
	if err := first.SaveManifest(); err != nil {
		t.Fatal(err)
	}
	created := first.Manifest.CreatedAt

	// Re-opening the same query on the same day must land in the same directory and
	// carry the accumulated cost forward rather than resetting it.
	second := open(t, root, "AI agents for SMBs")

	if second.Dir != first.Dir {
		t.Errorf("second Open used %q, want %q", second.Dir, first.Dir)
	}
	if !second.Resumed() {
		t.Error("Resumed() = false despite existing candidates")
	}
	if !second.Manifest.CreatedAt.Equal(created) {
		t.Error("CreatedAt was reset on resume")
	}
	if second.Manifest.Cost.InputTokens != 500 {
		t.Errorf("Cost.InputTokens = %d, want the earlier run's 500 carried forward",
			second.Manifest.Cost.InputTokens)
	}

	cands, err := second.LoadCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].ID != "ledgerly" {
		t.Errorf("LoadCandidates = %+v", cands)
	}
}

// Per-candidate caching is what makes stage 2 affordable to iterate on, so "not analysed
// yet" must be distinguishable from "failed to read".
func TestLoadAnalysisReportsAbsenceNotError(t *testing.T) {
	r := open(t, t.TempDir(), "q")

	_, found, err := r.LoadAnalysis("never-analysed")
	if err != nil {
		t.Fatalf("LoadAnalysis on a missing file returned an error: %v", err)
	}
	if found {
		t.Error("found = true for a candidate that was never analysed")
	}

	if err := r.SaveAnalysis(analysis("ledgerly", 72, model.CallMeeting, 0.8)); err != nil {
		t.Fatal(err)
	}

	got, found, err := r.LoadAnalysis("ledgerly")
	if err != nil || !found {
		t.Fatalf("LoadAnalysis after save: found=%v err=%v", found, err)
	}
	if got.Score.Total != 72 {
		t.Errorf("Score.Total = %d, want 72", got.Score.Total)
	}
}

// A corrupt file is a different problem from a missing one and must not be silently
// treated as "not analysed yet", which would quietly re-pay for the analysis.
func TestLoadAnalysisSurfacesCorruption(t *testing.T) {
	r := open(t, t.TempDir(), "q")
	path := filepath.Join(r.Dir, analysisDir, "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := r.LoadAnalysis("broken")
	if err == nil {
		t.Fatal("LoadAnalysis on a corrupt file returned no error")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error does not name the bad file: %v", err)
	}
}

func TestLoadAllAnalysesRanksByScore(t *testing.T) {
	r := open(t, t.TempDir(), "q")
	for _, a := range []model.Analysis{
		analysis("bravo", 50, model.CallWatch, 0.9),
		analysis("alpha", 88, model.CallMeeting, 0.9),
		analysis("delta", 50, model.CallWatch, 0.4), // same score, lower confidence
		analysis("charlie", 20, model.CallPass, 0.9),
	} {
		if err := r.SaveAnalysis(a); err != nil {
			t.Fatal(err)
		}
	}

	got, err := r.LoadAllAnalyses()
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for _, a := range got {
		order = append(order, a.CandidateID)
	}
	want := []string{"alpha", "bravo", "delta", "charlie"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v (score desc, then confidence desc)", order, want)
		}
	}
}

// Run output is committed, so it has to be diff-stable: re-rendering unchanged data must
// not produce a different file.
func TestWriteIndexIsStable(t *testing.T) {
	r := open(t, t.TempDir(), "AI agents for SMBs")
	r.Manifest.Candidates = 4
	r.Manifest.Screened = []ScreenedOut{
		{ID: "mcp360", Name: "Mcp360", Reason: "sells to agent developers, not businesses"},
		{ID: "agentmail", Name: "AgentMail", Reason: "developer infrastructure"},
	}
	r.RecordFailure("zulu", errFake("site unreachable"))
	r.RecordFailure("yankee", errFake("model never submitted"))
	if err := r.SaveManifest(); err != nil {
		t.Fatal(err)
	}

	analyses := []model.Analysis{
		analysis("alpha", 88, model.CallMeeting, 0.9),
		analysis("bravo", 50, model.CallWatch, 0.9),
		analysis("charlie", 20, model.CallPass, 0.9),
	}

	if err := r.WriteIndex(analyses); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(r.Dir, indexFile))
	if err != nil {
		t.Fatal(err)
	}

	// Failures live in a map; iterating it directly would reshuffle these lines and
	// produce noise diffs on output that had not changed.
	for i := 0; i < 8; i++ {
		if err := r.WriteIndex(analyses); err != nil {
			t.Fatal(err)
		}
		again, err := os.ReadFile(filepath.Join(r.Dir, indexFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("index.md is not stable across renders (iteration %d)", i)
		}
	}

	index := string(first)
	for _, want := range []string{
		"Take a meeting", "Watch", "Pass",
		"Screened out before analysis",
		"sells to agent developers",
		"Not analysed",
		"site unreachable",
		"memos/alpha.md",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.md is missing %q", want)
		}
	}
}

// An interrupted write must leave the previous good file rather than a truncated one
// that fails to parse on resume.
func TestWriteJSONIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")

	if err := writeJSON(path, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, map[string]int{"a": 2}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %q was left behind", e.Name())
		}
	}

	var back map[string]int
	if err := readJSON(path, &back); err != nil {
		t.Fatal(err)
	}
	if back["a"] != 2 {
		t.Errorf("value = %d, want 2", back["a"])
	}
}

func TestTracePathIsRelativeToTheRun(t *testing.T) {
	r := open(t, t.TempDir(), "q")

	abs, rel := r.TracePath("ledgerly")

	if rel != "trace/ledgerly.json" {
		t.Errorf("rel = %q, want trace/ledgerly.json", rel)
	}
	if !strings.HasSuffix(abs, filepath.Join(r.Dir, "trace", "ledgerly.json")) {
		t.Errorf("abs = %q, want it under the run dir", abs)
	}
	// The relative form is what gets recorded in the analysis and linked from the
	// memo, so a committed run stays self-contained wherever it is checked out.
	if filepath.IsAbs(rel) {
		t.Error("rel is absolute; committed runs would carry a machine-specific path")
	}
}

func TestAddCostTracksEscalationRate(t *testing.T) {
	r := open(t, t.TempDir(), "q")

	r.AddCost(model.Meta{PageFetch: "ok", InputTokens: 100})
	r.AddCost(model.Meta{PageFetch: "ok", InputTokens: 100})
	r.AddCost(model.Meta{PageFetch: "blocked", InputTokens: 100, WebFetches: 1})

	c := r.Manifest.Cost
	if c.LocalFetchOK != 2 || c.Escalated != 1 {
		t.Errorf("local=%d escalated=%d, want 2 and 1", c.LocalFetchOK, c.Escalated)
	}
	if c.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", c.InputTokens)
	}
}

func TestThousands(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{{0, "0"}, {42, "42"}, {999, "999"}, {1000, "1,000"}, {1234567, "1,234,567"}}
	for _, tc := range tests {
		if got := thousands(tc.in); got != tc.want {
			t.Errorf("thousands(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
