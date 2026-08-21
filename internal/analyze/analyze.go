// Package analyze is stage 2: turn a sourced candidate into a graded, evidence-backed
// analysis.
//
// The stage does three things in order — read the company's own site at tier 1, ask the
// model to research and grade it (escalating to tiers 2 and 3 as needed), then compute
// the score in Go from the model's grades.
package analyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Soumik43/emergence/internal/fetch"
	"github.com/Soumik43/emergence/internal/llm"
	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
)

// Analyzer runs stage 2 for one candidate at a time.
type Analyzer struct {
	LLM     *llm.Client
	Fetcher *fetch.Fetcher
	// SkipLocalFetch forces every page read to tier 2. Useful for isolating whether a
	// bad analysis came from thin local extraction or from the model.
	SkipLocalFetch bool
}

func New(client *llm.Client, fetcher *fetch.Fetcher) *Analyzer {
	return &Analyzer{LLM: client, Fetcher: fetcher}
}

// ErrRefused reports that a safety classifier declined the request. Surfaced distinctly
// because it is not a bug to retry — it needs a human to look at the input.
var ErrRefused = errors.New("model refused the request")

// Analyze produces the analysis for one candidate, writing its trace to tracePath.
//
// Partial failure is normal here and never fatal to a run: a company with an unreachable
// site and no findable founders still yields a memo, scored low with low confidence,
// which is the honest answer rather than a gap in the output.
func (a *Analyzer) Analyze(ctx context.Context, c model.Candidate, tracePath string) (model.Analysis, error) {
	// Tier 1: read the company's own site locally. A failure here is expected for a
	// minority of sites and is passed into the prompt so the model escalates itself.
	page := fetch.Result{URL: c.Website, Outcome: fetch.OutcomeTransport, Detail: "local fetch skipped"}
	if !a.SkipLocalFetch && c.Website != "" {
		page = a.Fetcher.Get(ctx, c.Website)
	}

	system, err := SystemPrompt()
	if err != nil {
		return model.Analysis{}, err
	}
	user, err := UserPrompt(c, page)
	if err != nil {
		return model.Analysis{}, err
	}

	res, err := a.LLM.Do(ctx, llm.Request{
		Label:      "analyze/" + c.ID,
		System:     system,
		User:       user,
		AnswerTool: AnswerTool(),
		WebSearch:  true,
		WebFetch:   true,
	}, tracePath)
	if err != nil {
		return model.Analysis{}, err
	}
	if res.Refused {
		return model.Analysis{}, fmt.Errorf("%w (%s)", ErrRefused, res.RefusalReason)
	}
	if len(res.Answer) == 0 {
		return model.Analysis{}, fmt.Errorf("analyze %s: model returned no analysis (said: %q)",
			c.ID, truncate(res.Text, 200))
	}

	var payload answerPayload
	if err := json.Unmarshal(res.Answer, &payload); err != nil {
		return model.Analysis{}, fmt.Errorf("analyze %s: decode answer: %w", c.ID, err)
	}

	return assemble(c, payload, page, res), nil
}

// assemble converts the model's payload into a model.Analysis and applies the score.
// Kept separate from Analyze so it can be tested against recorded payloads without an
// API key — see analyze_test.go.
func assemble(c model.Candidate, p answerPayload, page fetch.Result, res llm.Result) model.Analysis {
	an := model.Analysis{
		CandidateID: c.ID,
		Name:        firstNonEmpty(p.Name, c.Name),
		Website:     c.Website,
		AnalyzedAt:  time.Now().UTC(),
		Team:        toSection(p.Team),
		Product:     toSection(p.Product),
		Market:      toSection(p.Market),
		AnalystNote: strings.TrimSpace(p.AnalystNote),
		Meta: model.Meta{
			Model:           llm.DefaultModel,
			TraceFile:       res.TracePath,
			PageFetch:       string(page.Outcome),
			PageFetchDetail: page.Detail,
			WebSearches:     res.WebSearches,
			WebFetches:      res.WebFetches,
			InputTokens:     res.InputTokens,
			OutputTokens:    res.OutputTokens,
			DurationMS:      res.DurationMS,
		},
	}

	for _, r := range p.Risks {
		if strings.TrimSpace(r.Risk) == "" {
			continue
		}
		an.Risks = append(an.Risks, model.Risk{
			Risk:      r.Risk,
			Severity:  normaliseSeverity(r.Severity),
			Falsifier: r.Falsifier,
		})
	}

	for _, g := range p.Criteria {
		// Drop evidence entries with no URL. The scorer treats an empty evidence
		// list as absent, so this is what makes "cite a source or it doesn't count"
		// actually bite rather than being a request the model can ignore.
		var evidence []model.Evidence
		for _, e := range g.Evidence {
			if strings.TrimSpace(e.URL) == "" {
				continue
			}
			evidence = append(evidence, model.Evidence{Claim: e.Claim, URL: e.URL})
		}
		an.Criteria = append(an.Criteria, model.CriterionGrade{
			Key:           g.Key,
			Grade:         g.Grade,
			Justification: g.Justification,
			Evidence:      evidence,
			NoEvidence:    g.NoEvidence,
		})
	}

	an.Score = thesis.Score(an.Criteria)

	// The one-liner is the line a partner skims, so fall back to the model's plain
	// rewrite when it gave no product summary at all.
	an.Product.Summary = firstNonEmpty(an.Product.Summary, p.OneLiner, c.OneLiner)

	return an
}

func toSection(s sectionPayload) model.Section {
	out := model.Section{Summary: strings.TrimSpace(s.Summary)}
	for _, e := range s.Evidence {
		if strings.TrimSpace(e.URL) == "" {
			// Keep the claim but mark it unsourced, so the memo can show it as
			// such rather than presenting it with the same weight as a cited one.
			out.Evidence = append(out.Evidence, model.Evidence{Claim: e.Claim})
			continue
		}
		out.Evidence = append(out.Evidence, model.Evidence{Claim: e.Claim, URL: e.URL})
	}
	return out
}

// normaliseSeverity maps whatever the model returned onto the known set. Strict-mode
// enums make this nearly redundant, but a silent "" severity would render as a blank
// badge in the memo, so it defaults to major.
func normaliseSeverity(s string) model.Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "kill", "fatal":
		return model.SeverityKill
	case "minor", "low":
		return model.SeverityMinor
	default:
		return model.SeverityMajor
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
