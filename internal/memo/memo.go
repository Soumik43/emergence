// Package memo is stage 3: render a graded analysis into the one page a partner reads.
//
// It is deliberately free of LLM calls. Everything a memo says already exists in the
// analysis; this stage only decides what a reader sees first. That makes re-rendering
// every memo in a run instant and free, so the layout can be iterated on without paying
// to re-analyse anything.
package memo

import (
	"bytes"
	"embed"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
)

//go:embed templates/memo.md.tmpl
var templates embed.FS

var memoTmpl = template.Must(template.ParseFS(templates, "templates/memo.md.tmpl"))

// maxChangeMyMind caps the "what would change my mind" list. The brief asks for two or
// three; more than three is a list nobody acts on.
const maxChangeMyMind = 3

// Render turns an analysis into markdown.
func Render(a model.Analysis, c model.Candidate) (string, error) {
	var buf bytes.Buffer
	if err := memoTmpl.Execute(&buf, newView(a, c)); err != nil {
		return "", fmt.Errorf("render memo for %s: %w", a.CandidateID, err)
	}
	// Templates with conditionals leave ragged blank lines; markdown doesn't care but
	// a person reading the raw file does.
	return tidyBlankLines(buf.String()), nil
}

type view struct {
	Name          string
	Call          string
	Score         int
	ConfidencePct int
	Provisional   bool
	Website       string
	SourceQuery   string
	AnalyzedAt    string
	OneLine       string
	Rationale     string
	ChangeMyMind  []numbered

	Breakdown       []breakdownRow
	HasImputed      bool
	ImputedWeight   int
	MaxGrade        int
	NoEvidenceGrade int

	Product model.Section
	Team    model.Section
	Market  model.Section
	Risks   []model.Risk
	Signals []signalRow

	AnalystNote string

	PageFetch       string
	PageFetchDetail string
	WebSearches     int
	WebFetches      int
	Model           string
	TraceFile       string
	TraceRelPath    string
}

type numbered struct {
	N    int
	Text string
}

type breakdownRow struct {
	Label         string
	Grade         int
	Weight        int
	Points        string
	Imputed       bool
	Justification string
	Evidence      []model.Evidence
}

type signalRow struct {
	Date   string
	Detail string
	URL    string
}

func newView(a model.Analysis, c model.Candidate) view {
	v := view{
		Name:            a.Name,
		Call:            string(a.Score.Call),
		Score:           a.Score.Total,
		ConfidencePct:   int(math.Round(a.Score.Confidence * 100)),
		Provisional:     a.Score.Provisional,
		Website:         a.Website,
		SourceQuery:     c.Source.Query,
		AnalyzedAt:      a.AnalyzedAt.Format("2 Jan 2006"),
		OneLine:         firstNonEmpty(a.Product.Summary, c.OneLiner, "No description found."),
		MaxGrade:        thesis.MaxGrade,
		NoEvidenceGrade: thesis.NoEvidenceGrade,
		Product:         a.Product,
		Team:            a.Team,
		Market:          a.Market,
		Risks:           sortedRisks(a.Risks),
		AnalystNote:     a.AnalystNote,
		PageFetch:       fetchLabel(a.Meta.PageFetch),
		PageFetchDetail: a.Meta.PageFetchDetail,
		WebSearches:     a.Meta.WebSearches,
		WebFetches:      a.Meta.WebFetches,
		Model:           a.Meta.Model,
		TraceFile:       a.Meta.TraceFile,
		TraceRelPath:    traceRelPath(a.Meta.TraceFile),
	}

	// The one-liner heads the memo, so trim it to a sentence — a full product
	// paragraph there defeats the sixty-second read.
	v.OneLine = firstSentence(v.OneLine)

	justifications := map[string]model.CriterionGrade{}
	for _, g := range a.Criteria {
		justifications[g.Key] = g
	}

	for _, row := range a.Score.Breakdown {
		g := justifications[row.Key]
		note := g.Justification
		if row.Imputed {
			v.HasImputed = true
			v.ImputedWeight += row.Weight
			if note == "" {
				note = "No evidence found."
			} else {
				note = "No sourced evidence found. " + capitalise(note)
			}
		}
		v.Breakdown = append(v.Breakdown, breakdownRow{
			Label:         row.Label,
			Grade:         row.Grade,
			Weight:        row.Weight,
			Points:        trimFloat(row.Points),
			Imputed:       row.Imputed,
			Justification: note,
			Evidence:      g.Evidence,
		})
	}

	for _, s := range c.Signals {
		v.Signals = append(v.Signals, signalRow{
			Date:   s.Date.Format("2 Jan 2006"),
			Detail: s.Detail,
			URL:    s.URL,
		})
	}

	v.Rationale = rationale(a)
	for i, item := range changeMyMind(a) {
		v.ChangeMyMind = append(v.ChangeMyMind, numbered{N: i + 1, Text: item})
	}

	return v
}

// rationale explains the call in terms of the score that produced it.
//
// Composed in code rather than asked of the model, because the call is computed after
// the model has finished: the model grades criteria and never sees the total, so it
// cannot write a rationale for a verdict it doesn't know. Naming the strongest and
// weakest criteria is also the most useful two sentences available — it tells a partner
// which specific claim to attack.
func rationale(a model.Analysis) string {
	rows := append([]model.ScoredCriterion(nil), a.Score.Breakdown...)
	if len(rows) == 0 {
		return "No criteria were graded, so this score carries no information. Treat as unreviewed."
	}

	// Sort by grade, then by weight, so ties resolve toward what the thesis cares
	// about most.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Grade != rows[j].Grade {
			return rows[i].Grade > rows[j].Grade
		}
		return rows[i].Weight > rows[j].Weight
	})

	best, worst := rows[0], rows[len(rows)-1]

	var b strings.Builder
	switch a.Score.Call {
	case model.CallMeeting:
		fmt.Fprintf(&b, "Clears the bar at %d/100. ", a.Score.Total)
	case model.CallWatch:
		fmt.Fprintf(&b, "Lands at %d/100 — interesting, not yet compelling. ", a.Score.Total)
	default:
		fmt.Fprintf(&b, "Scores %d/100, below the bar. ", a.Score.Total)
	}

	if best.Grade > worst.Grade {
		fmt.Fprintf(&b, "Strongest on %s (%d/%d)", strings.ToLower(best.Label), best.Grade, thesis.MaxGrade)
		if best.Imputed {
			b.WriteString(" — though even that is an unevidenced default")
		}
		fmt.Fprintf(&b, "; weakest on %s (%d/%d)", strings.ToLower(worst.Label), worst.Grade, thesis.MaxGrade)
		if worst.Imputed {
			b.WriteString(", where nothing was found at all")
		}
		b.WriteString(". ")
	} else {
		fmt.Fprintf(&b, "Graded flat at %d/%d across every criterion, which usually means thin evidence rather than a uniform company. ",
			best.Grade, thesis.MaxGrade)
	}

	if kill := killRisks(a.Risks); len(kill) > 0 {
		fmt.Fprintf(&b, "One risk is rated fatal: %s ", ensurePeriod(kill[0].Risk))
	}

	if a.Score.Provisional {
		fmt.Fprintf(&b,
			"Confidence is %d%%: %d of 100 points of weight rest on evidence that could not be found, "+
				"so read this as a call on current evidence rather than a settled judgement.",
			int(math.Round(a.Score.Confidence*100)), imputedWeight(a.Score.Breakdown))
	}

	return strings.TrimSpace(b.String())
}

// changeMyMind builds the falsifier list.
//
// Risk falsifiers come first, worst risk first, because those are the questions a
// partner would actually ask in the meeting. Missing evidence fills any remainder: on a
// thin candidate "find out who the founders are" genuinely is the thing that would move
// the call, and saying so is more honest than padding the list with invented risks.
func changeMyMind(a model.Analysis) []string {
	var out []string
	seen := map[string]bool{}

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(out) >= maxChangeMyMind {
			return
		}
		if key := strings.ToLower(s); !seen[key] {
			seen[key] = true
			out = append(out, ensurePeriod(s))
		}
	}

	for _, r := range sortedRisks(a.Risks) {
		add(r.Falsifier)
	}

	// Highest-weight gaps first: absent evidence on founder proximity moves the score
	// far more than absent evidence on traction.
	gaps := append([]model.ScoredCriterion(nil), a.Score.Breakdown...)
	sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].Weight > gaps[j].Weight })
	for _, row := range gaps {
		if row.Imputed {
			add(fmt.Sprintf("Evidence on %s — none was found, and it carries %d of 100 points of weight.",
				strings.ToLower(row.Label), row.Weight))
		}
	}

	if len(out) == 0 {
		add("Nothing specific — the analysis found no falsifiable risk, which is itself worth a second look.")
	}
	return out
}

// sortedRisks orders risks fatal-first so the memo leads with what actually matters.
func sortedRisks(risks []model.Risk) []model.Risk {
	out := append([]model.Risk(nil), risks...)
	rank := map[model.Severity]int{
		model.SeverityKill:  0,
		model.SeverityMajor: 1,
		model.SeverityMinor: 2,
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Severity] < rank[out[j].Severity] })
	return out
}

func killRisks(risks []model.Risk) []model.Risk {
	var out []model.Risk
	for _, r := range risks {
		if r.Severity == model.SeverityKill {
			out = append(out, r)
		}
	}
	return out
}

func imputedWeight(rows []model.ScoredCriterion) int {
	var sum int
	for _, r := range rows {
		if r.Imputed {
			sum += r.Weight
		}
	}
	return sum
}

// fetchLabel turns a fetch outcome into something a partner can read without knowing
// the pipeline's internals.
func fetchLabel(outcome string) string {
	switch outcome {
	case "ok":
		return "our own fetcher"
	case "blocked":
		return "server-side fetch (local fetch was blocked)"
	case "js_shell":
		return "server-side fetch (page renders client-side)"
	case "not_html":
		return "server-side fetch (not an HTML page)"
	case "transport", "":
		return "server-side fetch (site unreachable locally)"
	default:
		return outcome
	}
}

// traceRelPath links from a memo to its trace. Memos live in <run>/memos/ and traces in
// <run>/trace/, so the link has to climb one level.
func traceRelPath(traceFile string) string {
	if traceFile == "" {
		return "#"
	}
	return path.Join("..", traceFile)
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	// Only split on a period followed by a space, so "Inc. Files returns" doesn't
	// truncate at the abbreviation.
	if i := strings.Index(s, ". "); i > 0 && i < len(s)-2 {
		return s[:i+1]
	}
	return s
}

// capitalise upper-cases the first letter, so a sentence appended after one of our own
// doesn't read as "No sourced evidence found. graded because…".
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func ensurePeriod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	switch s[len(s)-1] {
	case '.', '?', '!':
		return s
	}
	return s + "."
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// trimFloat renders 12.0 as "12" and 12.5 as "12.5", so the score table reads cleanly.
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	return strings.TrimSuffix(s, ".0")
}

// tidyBlankLines collapses runs of blank lines left behind by template conditionals.
func tidyBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	var blanks int
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blanks = 0
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}
