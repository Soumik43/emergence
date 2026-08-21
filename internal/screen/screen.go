// Package screen drops candidates whose own one-line description puts them in a
// different market, before the expensive analysis stage runs.
//
// It exists because HN's search is keyword-based: a query like "AI agents for SMBs"
// matches on "AI" and "agent" and effectively ignores "for SMBs", so a live run returned
// twelve agent-developer tools and no SMB software. See
// docs/decisions/004-relevance-screen.md.
//
// The bar is deliberately low. This is not a scoring stage — the thesis rubric in stage 2
// is where companies get judged. Only a candidate the description itself rules out is
// dropped; anything uncertain is kept.
package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/Soumik43/emergence/internal/llm"
	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
	"github.com/Soumik43/emergence/prompts"
)

const answerToolName = "submit_screen"

var systemTmpl = template.Must(template.ParseFS(prompts.FS, "screen_system.md.tmpl"))

// Verdict is the screen's judgement on one candidate.
type Verdict string

const (
	// VerdictIn means the buyer is plausibly an SMB. Kept.
	VerdictIn Verdict = "in"
	// VerdictAdjacent means unclear from the description. Kept — a wrong drop costs a
	// whole memo, a wrong keep costs one analysis.
	VerdictAdjacent Verdict = "adjacent"
	// VerdictOut means the description itself puts this in another market. Dropped.
	VerdictOut Verdict = "out"
)

// Dropped records a screened-out candidate and why, so a run can report what it
// discarded instead of just being short.
type Dropped struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Screener runs the relevance screen.
type Screener struct {
	LLM *llm.Client
}

func New(client *llm.Client) *Screener { return &Screener{LLM: client} }

// Screen partitions candidates into kept and dropped.
//
// It fails soft: any error means every candidate is kept and the error is returned for
// logging. A broken screen must not empty the pipeline — the worst outcome of the screen
// being unavailable is that stage 2 costs what it would have cost anyway.
func (s *Screener) Screen(ctx context.Context, candidates []model.Candidate, tracePath string) (kept []model.Candidate, dropped []Dropped, err error) {
	if len(candidates) == 0 {
		return candidates, nil, nil
	}

	system, err := s.systemPrompt()
	if err != nil {
		return candidates, nil, err
	}

	// Screen at low effort: this is a one-line classification, not a judgement call,
	// and the whole point is that it costs a fraction of an analysis.
	client := *s.LLM
	client.Effort = anthropic.OutputConfigEffortLow
	client.MaxTokens = 4000

	res, err := client.Do(ctx, llm.Request{
		Label:      "screen",
		System:     system,
		User:       userPrompt(candidates),
		AnswerTool: answerTool(),
		// No web search: the description is all this decision needs, and searching
		// would make the screen cost what the analysis costs.
	}, tracePath)
	if err != nil {
		return candidates, nil, fmt.Errorf("screen: %w", err)
	}
	if res.Refused {
		return candidates, nil, fmt.Errorf("screen refused: %s", res.RefusalReason)
	}
	if len(res.Answer) == 0 {
		return candidates, nil, fmt.Errorf("screen returned no verdicts")
	}

	var payload struct {
		Verdicts []struct {
			ID      string `json:"id"`
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		} `json:"verdicts"`
	}
	if err := json.Unmarshal(res.Answer, &payload); err != nil {
		return candidates, nil, fmt.Errorf("screen: decode verdicts: %w", err)
	}

	verdicts := make(map[string]struct {
		verdict Verdict
		reason  string
	}, len(payload.Verdicts))
	for _, v := range payload.Verdicts {
		verdicts[v.ID] = struct {
			verdict Verdict
			reason  string
		}{normalise(v.Verdict), strings.TrimSpace(v.Reason)}
	}

	for _, c := range candidates {
		v, judged := verdicts[c.ID]
		// A candidate the model skipped is kept. Silence is not a verdict, and the
		// safe default is to let stage 2 look at it.
		if !judged || v.verdict != VerdictOut {
			kept = append(kept, c)
			continue
		}
		reason := v.reason
		if reason == "" {
			reason = "screened out as off-segment (no reason given)"
		}
		dropped = append(dropped, Dropped{ID: c.ID, Name: c.Name, Reason: reason})
	}

	// If the screen wanted to drop everything, something is wrong with the screen
	// rather than with every candidate. Keep them all and say so.
	if len(kept) == 0 {
		return candidates, nil, fmt.Errorf(
			"screen rejected all %d candidates, which is more likely a bad screen than a bad query; keeping them all",
			len(candidates))
	}

	return kept, dropped, nil
}

func (s *Screener) systemPrompt() (string, error) {
	var buf bytes.Buffer
	err := systemTmpl.Execute(&buf, struct {
		Statement  string
		AnswerTool string
	}{thesis.Statement, answerToolName})
	if err != nil {
		return "", fmt.Errorf("render screen prompt: %w", err)
	}
	return buf.String(), nil
}

// userPrompt lists every candidate in one message, so the model sees the whole set and
// can calibrate against it rather than judging each in isolation.
func userPrompt(candidates []model.Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Screen these %d companies. Return a verdict for every one.\n\n", len(candidates))
	for _, c := range candidates {
		fmt.Fprintf(&b, "- id: `%s`\n  name: %s\n  site: %s\n  describes itself as: %s\n\n",
			c.ID, c.Name, c.Website, c.OneLiner)
	}
	return b.String()
}

func answerTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        answerToolName,
		Description: anthropic.String("Return one relevance verdict per company."),
		Strict:      anthropic.Bool(true),
		InputSchema: anthropic.ToolInputSchemaParam{
			Required: []string{"verdicts"},
			Properties: map[string]any{
				"verdicts": map[string]any{
					"type":        "array",
					"description": "One entry per company, in the order given.",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"id", "verdict", "reason"},
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "The candidate id exactly as given.",
							},
							"verdict": map[string]any{
								"type": "string",
								"enum": []string{string(VerdictIn), string(VerdictAdjacent), string(VerdictOut)},
							},
							"reason": map[string]any{
								"type":        "string",
								"description": "At most fifteen words, naming the buyer you think it sells to. Shown to a partner as the justification for a drop.",
							},
						},
					},
				},
			},
			ExtraFields: map[string]any{"additionalProperties": false},
		},
	}
}

// normalise maps the model's string onto a known verdict, defaulting to adjacent — the
// verdict that keeps the candidate.
func normalise(s string) Verdict {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in":
		return VerdictIn
	case "out":
		return VerdictOut
	default:
		return VerdictAdjacent
	}
}
