package analyze

import (
	"github.com/anthropics/anthropic-sdk-go"

	"github.com/Soumik43/emergence/internal/thesis"
)

// AnswerToolName is the tool the model must call to deliver its analysis.
const AnswerToolName = "submit_analysis"

// answerPayload is what the model returns. It mirrors model.Analysis minus the fields
// this code owns (the score, the metadata, the candidate ID) — the model grades, Go
// computes. See docs/decisions/003-score-in-code.md.
type answerPayload struct {
	// Name and OneLiner let the model correct the values sourcing guessed from an HN
	// title, which is frequently wrong.
	Name     string `json:"name"`
	OneLiner string `json:"one_liner"`

	Team    sectionPayload `json:"team"`
	Product sectionPayload `json:"product"`
	Market  sectionPayload `json:"market"`

	Risks    []riskPayload    `json:"risks"`
	Criteria []criterionGrade `json:"criteria"`

	AnalystNote string `json:"analyst_note"`
}

type sectionPayload struct {
	Summary  string            `json:"summary"`
	Evidence []evidencePayload `json:"evidence"`
}

type evidencePayload struct {
	Claim string `json:"claim"`
	URL   string `json:"url"`
}

type riskPayload struct {
	Risk      string `json:"risk"`
	Severity  string `json:"severity"`
	Falsifier string `json:"falsifier"`
}

type criterionGrade struct {
	Key           string            `json:"key"`
	Grade         int               `json:"grade"`
	Justification string            `json:"justification"`
	Evidence      []evidencePayload `json:"evidence"`
	NoEvidence    bool              `json:"no_evidence"`
}

// AnswerTool builds the tool definition.
//
// Declared strict with additionalProperties:false so the input validates against this
// schema server-side. That is worth the verbosity of a hand-written schema: without it
// every field needs defensive parsing on arrival, and a malformed grade array becomes a
// silent zero rather than a validation error.
//
// The schema is generated from thesis.Criteria rather than hardcoded, so adding a
// criterion to the thesis automatically requires the model to grade it.
func AnswerTool() anthropic.ToolParam {
	criterionKeys := make([]string, 0, len(thesis.Criteria))
	for _, c := range thesis.Criteria {
		criterionKeys = append(criterionKeys, c.Key)
	}

	evidenceSchema := map[string]any{
		"type":        "array",
		"description": "Sourced claims. Every entry needs a URL you actually retrieved this session. An empty array means you found nothing, which is a valid and useful answer.",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"claim", "url"},
			"properties": map[string]any{
				"claim": map[string]any{
					"type":        "string",
					"description": "One specific fact, stated so a partner could verify it against the URL.",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "The page this fact came from. Never a URL you did not read.",
				},
			},
		},
	}

	section := func(desc string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"summary", "evidence"},
			"properties": map[string]any{
				"summary":  map[string]any{"type": "string", "description": desc},
				"evidence": evidenceSchema,
			},
		}
	}

	return anthropic.ToolParam{
		Name: AnswerToolName,
		Description: anthropic.String(
			"Submit the completed analysis. Call this exactly once, after researching. " +
				"Everything a partner will read comes from this call."),
		Strict: anthropic.Bool(true),
		InputSchema: anthropic.ToolInputSchemaParam{
			Required: []string{
				"name", "one_liner", "team", "product", "market",
				"risks", "criteria", "analyst_note",
			},
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The company's actual name, corrected from its own site. Sourcing guessed this from a Hacker News title and is often wrong.",
				},
				"one_liner": map[string]any{
					"type":        "string",
					"description": "What the company does, in one plain sentence of your own words. Not their tagline.",
				},
				"team":    section("Founder backgrounds by name, prior roles, prior exits, and specifically whether they have done the workflow being automated. \"Technical\" is not a background."),
				"product": section("What the product actually does, in plain language, for whom, at what price if findable. Be explicit about whether it acts or only suggests."),
				"market":  section("A size hint with a stated basis, one or two named competitors, and why this is buildable now but was not three years ago."),
				"risks": map[string]any{
					"type":        "array",
					"description": "What would kill this company. Name mechanisms, not categories — \"execution risk\" is filler.",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"risk", "severity", "falsifier"},
						"properties": map[string]any{
							"risk": map[string]any{"type": "string"},
							"severity": map[string]any{
								"type":        "string",
								"enum":        []string{"kill", "major", "minor"},
								"description": "kill = the company does not work if this is true; major = materially changes the outcome; minor = worth noting.",
							},
							"falsifier": map[string]any{
								"type":        "string",
								"description": "The specific question or check that would resolve this risk either way. This becomes the memo's \"what would change my mind\".",
							},
						},
					},
				},
				// Note: no minItems/maxItems, and no minimum/maximum on grade below.
				// Strict mode validates against a restricted schema subset and
				// rejects unsupported keywords with a 400 rather than ignoring them,
				// which would fail the whole run rather than degrade one field. The
				// constraints are stated in the descriptions instead, and enforced
				// where they actually have to hold: thesis.Score clamps grades to
				// 0–5 and scores an omitted criterion as absent. Both are tested.
				"criteria": map[string]any{
					"type":        "array",
					"description": "One entry per thesis criterion — exactly " + itoa(len(thesis.Criteria)) + ", one for each key in the enum below. Omitting one does not skip it; it is scored as absent.",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"key", "grade", "justification", "evidence", "no_evidence"},
						"properties": map[string]any{
							"key": map[string]any{
								"type": "string",
								"enum": criterionKeys,
							},
							"grade": map[string]any{
								"type": "integer",
								"description": "An integer from 0 to " + itoa(thesis.MaxGrade) +
									" against this criterion's anchors. " + itoa(thesis.MaxGrade) +
									" is rare and needs direct evidence; 2 is the honest grade for most seed companies on most criteria.",
							},
							"justification": map[string]any{
								"type":        "string",
								"description": "One or two sentences on why this grade and not the one above it.",
							},
							"evidence": evidenceSchema,
							"no_evidence": map[string]any{
								"type":        "boolean",
								"description": "True when you found nothing to grade against. Preferred over inventing a middling grade — absence is scored explicitly downstream.",
							},
						},
					},
				},
				"analyst_note": map[string]any{
					"type":        "string",
					"description": "Something real the six criteria cannot express. Empty string rather than padding.",
				},
			},
			ExtraFields: map[string]any{
				// Required by strict mode, and it also stops the model inventing
				// extra top-level fields that would silently vanish on decode.
				"additionalProperties": false,
			},
		},
	}
}

// itoa avoids pulling strconv in for one call site in a schema string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
