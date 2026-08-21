// Package run owns a run directory: the on-disk boundary between pipeline stages.
//
// Stages never pass data to each other in memory. Each one reads the previous stage's
// files and writes its own, which is what makes the pipeline replayable — you can re-run
// memo rendering without re-analysing, or resume an interrupted analysis without
// re-paying for the candidates already done.
//
// Layout:
//
//	runs/<slug>-<date>/
//	  manifest.json      what was asked for, and what each stage has produced
//	  candidates.json    stage 1
//	  analysis/<id>.json stage 2, one file per candidate so resume is per-candidate
//	  memos/<id>.md      stage 3
//	  trace/<id>.json    the model exchanges behind each analysis
//	  index.md           ranked table of every memo — the file a partner opens first
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Soumik43/emergence/internal/model"
)

// Run is an open run directory.
type Run struct {
	Dir      string
	Manifest Manifest
}

// Manifest records what was asked for and what has been produced, so a partial run is
// resumable and a finished one is self-describing.
type Manifest struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Source    string    `json:"source"`
	Limit     int       `json:"limit"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Thesis is copied in so a committed run stays interpretable after the thesis
	// changes. Without it, an old run's scores silently refer to a rubric that no
	// longer exists.
	Thesis string `json:"thesis"`

	Candidates int `json:"candidates"`
	Analysed   int `json:"analysed"`
	Failed     int `json:"failed"`

	// Failures records why individual candidates dropped out, so a run with 14 of 18
	// memos explains the missing four instead of just being short.
	Failures map[string]string `json:"failures,omitempty"`

	// Screened records candidates the relevance screen removed before analysis, with
	// its reason. Kept for the same reason as Failures: a sourcing layer that silently
	// discards half its results is indistinguishable from one that never found them.
	Screened []ScreenedOut `json:"screened_out,omitempty"`

	// Cost aggregates the run's model usage. Committed because "what did this cost"
	// is the first question anyone asks about an LLM pipeline.
	Cost Cost `json:"cost"`
}

// ScreenedOut is a candidate the relevance screen removed, and why.
type ScreenedOut struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Cost is the run's aggregate model usage.
type Cost struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	WebSearches  int `json:"web_searches"`
	WebFetches   int `json:"web_fetches"`
	// LocalFetchOK counts candidates whose site tier 1 read successfully. The ratio
	// against Escalated is how you tell whether the local fetcher is earning its keep.
	LocalFetchOK int `json:"local_fetch_ok"`
	Escalated    int `json:"escalated"`
}

const (
	candidatesFile = "candidates.json"
	manifestFile   = "manifest.json"
	analysisDir    = "analysis"
	memosDir       = "memos"
	traceDir       = "trace"
	indexFile      = "index.md"
)

// ID builds the deterministic run directory name for a query.
//
// Deterministic on purpose: running the same query on the same day resumes that run
// rather than starting a parallel one, which makes resume the default behaviour instead
// of a flag someone has to remember.
func ID(query string, day time.Time) string {
	s := slug(query)
	if s == "" {
		s = "run"
	}
	return s + "-" + day.Format("2006-01-02")
}

// Open loads an existing run, or creates it if absent.
func Open(root, query, source string, limit int, thesisStatement string) (*Run, error) {
	dir := filepath.Join(root, ID(query, time.Now()))
	for _, sub := range []string{analysisDir, memosDir, traceDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create run dir: %w", err)
		}
	}

	r := &Run{Dir: dir}

	// An existing manifest means this is a resume; keep its creation time and
	// accumulated cost rather than resetting them.
	if blob, err := os.ReadFile(filepath.Join(dir, manifestFile)); err == nil {
		if err := json.Unmarshal(blob, &r.Manifest); err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
	} else {
		r.Manifest = Manifest{CreatedAt: time.Now().UTC()}
	}

	r.Manifest.ID = ID(query, time.Now())
	r.Manifest.Query = query
	r.Manifest.Source = source
	r.Manifest.Limit = limit
	r.Manifest.Thesis = thesisStatement
	if r.Manifest.Failures == nil {
		r.Manifest.Failures = map[string]string{}
	}

	return r, nil
}

// Resumed reports whether this run directory already held candidates when opened.
func (r *Run) Resumed() bool {
	_, err := os.Stat(filepath.Join(r.Dir, candidatesFile))
	return err == nil
}

func (r *Run) SaveCandidates(cands []model.Candidate) error {
	r.Manifest.Candidates = len(cands)
	return writeJSON(filepath.Join(r.Dir, candidatesFile), cands)
}

func (r *Run) LoadCandidates() ([]model.Candidate, error) {
	var out []model.Candidate
	if err := readJSON(filepath.Join(r.Dir, candidatesFile), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Run) SaveAnalysis(a model.Analysis) error {
	return writeJSON(filepath.Join(r.Dir, analysisDir, a.CandidateID+".json"), a)
}

// LoadAnalysis reads a cached analysis. The bool reports presence, so a caller can
// distinguish "not analysed yet" from "failed to read".
func (r *Run) LoadAnalysis(id string) (model.Analysis, bool, error) {
	var a model.Analysis
	err := readJSON(filepath.Join(r.Dir, analysisDir, id+".json"), &a)
	switch {
	case os.IsNotExist(err):
		return a, false, nil
	case err != nil:
		return a, false, err
	}
	return a, true, nil
}

// LoadAllAnalyses reads every analysis in the run, ordered by score descending — the
// order a partner wants to read them in.
func (r *Run) LoadAllAnalyses() ([]model.Analysis, error) {
	entries, err := os.ReadDir(filepath.Join(r.Dir, analysisDir))
	if err != nil {
		return nil, err
	}

	var out []model.Analysis
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var a model.Analysis
		if err := readJSON(filepath.Join(r.Dir, analysisDir, e.Name()), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score.Total != out[j].Score.Total {
			return out[i].Score.Total > out[j].Score.Total
		}
		// Break ties on confidence, then name, so ordering is stable across runs.
		if out[i].Score.Confidence != out[j].Score.Confidence {
			return out[i].Score.Confidence > out[j].Score.Confidence
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (r *Run) SaveMemo(id, markdown string) error {
	path := filepath.Join(r.Dir, memosDir, id+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(markdown), 0o644)
}

// TracePath is where an analysis's exchange log goes, as a path relative to the run
// directory — that relative form is what gets recorded in the analysis and rendered as a
// link in the memo, so a committed run stays self-contained wherever it's checked out.
func (r *Run) TracePath(id string) (abs, rel string) {
	rel = filepath.ToSlash(filepath.Join(traceDir, id+".json"))
	return filepath.Join(r.Dir, rel), rel
}

// RecordFailure notes why a candidate produced no memo.
func (r *Run) RecordFailure(id string, err error) {
	if r.Manifest.Failures == nil {
		r.Manifest.Failures = map[string]string{}
	}
	r.Manifest.Failures[id] = err.Error()
	r.Manifest.Failed = len(r.Manifest.Failures)
}

// AddCost accumulates usage from one analysis.
func (r *Run) AddCost(m model.Meta) {
	r.Manifest.Cost.InputTokens += m.InputTokens
	r.Manifest.Cost.OutputTokens += m.OutputTokens
	r.Manifest.Cost.WebSearches += m.WebSearches
	r.Manifest.Cost.WebFetches += m.WebFetches
	if m.PageFetch == "ok" {
		r.Manifest.Cost.LocalFetchOK++
	} else {
		r.Manifest.Cost.Escalated++
	}
}

func (r *Run) SaveManifest() error {
	r.Manifest.UpdatedAt = time.Now().UTC()
	return writeJSON(filepath.Join(r.Dir, manifestFile), r.Manifest)
}

// writeJSON writes indented JSON via a temp file and rename, so an interrupted run
// leaves the previous good file rather than a half-written one that fails to parse on
// resume. Indented because these files are read in diffs by people.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	blob = append(blob, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(blob, v); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func slug(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
