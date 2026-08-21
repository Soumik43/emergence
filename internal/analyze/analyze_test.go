package analyze

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Soumik43/emergence/internal/fetch"
	"github.com/Soumik43/emergence/internal/llm"
	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
)

func testCandidate() model.Candidate {
	return model.Candidate{
		ID:       "ledgerly",
		Name:     "Ledgerly",
		Website:  "https://ledgerly.com",
		OneLiner: "bookkeeping autopilot for restaurants",
		Signals: []model.Signal{{
			Kind:   model.SignalHNLaunch,
			Detail: "Show HN launch — 140 points, 70 comments",
			Date:   time.Now().Add(-30 * 24 * time.Hour),
			URL:    "https://news.ycombinator.com/item?id=1",
		}},
		Source: model.SourceRef{Name: "hackernews", Query: "AI agents for SMBs"},
	}
}

// gradeAll builds a full, evidence-backed criteria payload at one grade.
func gradeAll(grade int) []criterionGrade {
	out := make([]criterionGrade, 0, len(thesis.Criteria))
	for _, c := range thesis.Criteria {
		out = append(out, criterionGrade{
			Key:           c.Key,
			Grade:         grade,
			Justification: "graded from evidence",
			Evidence:      []evidencePayload{{Claim: "a fact", URL: "https://ledgerly.com/about"}},
		})
	}
	return out
}

func okPage() fetch.Result {
	return fetch.Result{
		URL:     "https://ledgerly.com",
		Outcome: fetch.OutcomeOK,
		Title:   "Ledgerly",
		Text:    "We close your books automatically.",
	}
}

func TestAssembleHappyPath(t *testing.T) {
	payload := answerPayload{
		Name:     "Ledgerly Inc.",
		OneLiner: "Files restaurant tax returns without a bookkeeper.",
		Team: sectionPayload{
			Summary:  "Two founders, one ex-restaurant CFO.",
			Evidence: []evidencePayload{{Claim: "Founder was CFO at a 30-site group", URL: "https://linkedin.com/in/x"}},
		},
		Product: sectionPayload{Summary: "Categorises transactions and files returns."},
		Market:  sectionPayload{Summary: "700k US restaurants; competes with Bench."},
		Risks: []riskPayload{
			{Risk: "Intuit ships this as a QuickBooks feature", Severity: "kill", Falsifier: "Ask whether QBO's 2026 roadmap includes auto-filing."},
			{Risk: "Filing errors create liability", Severity: "major", Falsifier: "Ask who carries the penalty when it files wrong."},
		},
		Criteria:    gradeAll(4),
		AnalystNote: "  Worth noting they price per location.  ",
	}

	got := assemble(testCandidate(), payload, okPage(), llm.Result{
		TracePath: "trace/ledgerly.json", WebSearches: 7, InputTokens: 1000, OutputTokens: 500,
	})

	// The model's corrected name wins: sourcing guessed from an HN title.
	if got.Name != "Ledgerly Inc." {
		t.Errorf("Name = %q, want the model's corrected name", got.Name)
	}
	if got.CandidateID != "ledgerly" {
		t.Errorf("CandidateID = %q, want the sourcing ID to survive", got.CandidateID)
	}
	if got.Score.Total != 80 {
		t.Errorf("Score.Total = %d, want 80 (grade 4/5 across all weights)", got.Score.Total)
	}
	if got.Score.Call != model.CallMeeting {
		t.Errorf("Call = %q, want %q", got.Score.Call, model.CallMeeting)
	}
	if got.Score.Confidence != 1 {
		t.Errorf("Confidence = %v, want 1", got.Score.Confidence)
	}
	if got.AnalystNote != "Worth noting they price per location." {
		t.Errorf("AnalystNote = %q, want it trimmed", got.AnalystNote)
	}
	if len(got.Risks) != 2 || got.Risks[0].Severity != model.SeverityKill {
		t.Errorf("Risks = %+v", got.Risks)
	}
	// Provenance has to survive into the analysis, or memos can't be spot-checked.
	if got.Meta.TraceFile != "trace/ledgerly.json" {
		t.Errorf("Meta.TraceFile = %q", got.Meta.TraceFile)
	}
	if got.Meta.PageFetch != string(fetch.OutcomeOK) {
		t.Errorf("Meta.PageFetch = %q, want ok", got.Meta.PageFetch)
	}
	if got.Meta.Model == "" {
		t.Error("Meta.Model is empty")
	}
}

// The central honesty rule: a grade citing no URL must not count. This is enforced here
// (by dropping the evidence) and in thesis.Score (by treating empty evidence as absent);
// the two together are what make "cite a source" more than a polite request.
func TestAssembleDropsUnsourcedCriterionEvidence(t *testing.T) {
	criteria := gradeAll(5)
	criteria[0].Evidence = []evidencePayload{{Claim: "the founders seem technical", URL: ""}}
	criteria[1].Evidence = []evidencePayload{
		{Claim: "unsourced hunch", URL: "   "},
		{Claim: "real fact", URL: "https://ledgerly.com/team"},
	}

	got := assemble(testCandidate(), answerPayload{Criteria: criteria}, okPage(), llm.Result{})

	if len(got.Criteria[0].Evidence) != 0 {
		t.Errorf("criterion 0 kept unsourced evidence: %+v", got.Criteria[0].Evidence)
	}
	if !got.Score.Breakdown[0].Imputed {
		t.Error("criterion 0 was not scored as absent despite having no sourced evidence")
	}
	if got.Score.Breakdown[0].Grade != thesis.NoEvidenceGrade {
		t.Errorf("criterion 0 grade = %d, want the no-evidence grade %d",
			got.Score.Breakdown[0].Grade, thesis.NoEvidenceGrade)
	}

	// A criterion with one bad and one good citation keeps the good one and its grade.
	if len(got.Criteria[1].Evidence) != 1 || got.Criteria[1].Evidence[0].URL == "" {
		t.Errorf("criterion 1 evidence = %+v, want only the sourced entry", got.Criteria[1].Evidence)
	}
	if got.Score.Breakdown[1].Imputed {
		t.Error("criterion 1 should keep its grade — it has one real source")
	}

	// Whitespace-only URLs must be treated as absent, not as valid sources.
	if got.Score.Confidence >= 1 {
		t.Errorf("Confidence = %v, want < 1 with a criterion missing evidence", got.Score.Confidence)
	}
}

// In the narrative sections an unsourced claim is kept but stripped of its URL, so the
// memo can render it as unsourced instead of silently dropping the model's reasoning.
func TestAssembleKeepsUnsourcedSectionClaimsMarked(t *testing.T) {
	payload := answerPayload{
		Team: sectionPayload{
			Summary: "Solo founder.",
			Evidence: []evidencePayload{
				{Claim: "ex-Stripe", URL: "https://linkedin.com/in/x"},
				{Claim: "probably strong at distribution", URL: ""},
			},
		},
		Criteria: gradeAll(3),
	}

	got := assemble(testCandidate(), payload, okPage(), llm.Result{})

	if len(got.Team.Evidence) != 2 {
		t.Fatalf("Team.Evidence has %d entries, want both kept", len(got.Team.Evidence))
	}
	if got.Team.Evidence[1].URL != "" {
		t.Error("unsourced section claim should carry no URL")
	}
	if got.Team.Evidence[1].Claim == "" {
		t.Error("unsourced section claim lost its text; the memo needs it to mark it")
	}
}

func TestAssembleHandlesDegenerateModelOutput(t *testing.T) {
	t.Run("empty payload still yields a scored analysis", func(t *testing.T) {
		got := assemble(testCandidate(), answerPayload{}, okPage(), llm.Result{})

		if got.Score.Call != model.CallPass {
			t.Errorf("Call = %q, want Pass on an empty analysis", got.Score.Call)
		}
		if !got.Score.Provisional {
			t.Error("Provisional = false; an evidence-free analysis must be flagged")
		}
		if len(got.Score.Breakdown) != len(thesis.Criteria) {
			t.Errorf("breakdown has %d rows, want one per criterion", len(got.Score.Breakdown))
		}
		// The candidate's own details must survive so the memo is still identifiable.
		if got.Name != "Ledgerly" || got.Website != "https://ledgerly.com" {
			t.Errorf("candidate identity lost: %q / %q", got.Name, got.Website)
		}
	})

	t.Run("blank risks are dropped and severity defaults", func(t *testing.T) {
		payload := answerPayload{
			Risks: []riskPayload{
				{Risk: "   ", Severity: "kill"},
				{Risk: "real risk", Severity: ""},
				{Risk: "another", Severity: "FATAL"},
				{Risk: "small one", Severity: "low"},
			},
			Criteria: gradeAll(3),
		}

		got := assemble(testCandidate(), payload, okPage(), llm.Result{})

		if len(got.Risks) != 3 {
			t.Fatalf("Risks = %d, want 3 (the blank one dropped)", len(got.Risks))
		}
		if got.Risks[0].Severity != model.SeverityMajor {
			t.Errorf("empty severity = %q, want major as the default", got.Risks[0].Severity)
		}
		if got.Risks[1].Severity != model.SeverityKill {
			t.Errorf("\"FATAL\" = %q, want kill", got.Risks[1].Severity)
		}
		if got.Risks[2].Severity != model.SeverityMinor {
			t.Errorf("\"low\" = %q, want minor", got.Risks[2].Severity)
		}
	})

	t.Run("blank one-liner falls back through model then sourcing", func(t *testing.T) {
		got := assemble(testCandidate(), answerPayload{Criteria: gradeAll(2)}, okPage(), llm.Result{})

		if got.Product.Summary != "bookkeeping autopilot for restaurants" {
			t.Errorf("Product.Summary = %q, want the sourcing one-liner as last resort", got.Product.Summary)
		}
	})
}

// A dead site must produce a memo that says so, not a gap in the output.
func TestAssembleRecordsFailedPageFetch(t *testing.T) {
	dead := fetch.Result{
		URL: "https://ledgerly.com", Outcome: fetch.OutcomeBlocked, Detail: "http 403",
	}

	got := assemble(testCandidate(), answerPayload{Criteria: gradeAll(2)}, dead, llm.Result{})

	if got.Meta.PageFetch != string(fetch.OutcomeBlocked) {
		t.Errorf("Meta.PageFetch = %q, want blocked", got.Meta.PageFetch)
	}
	if got.Meta.PageFetchDetail != "http 403" {
		t.Errorf("Meta.PageFetchDetail = %q, want the reason preserved", got.Meta.PageFetchDetail)
	}
}

// The prompt has to carry every criterion, or the model grades a rubric the scorer
// doesn't use — the exact drift the single-source-of-truth design exists to prevent.
func TestSystemPromptCoversTheWholeThesis(t *testing.T) {
	got, err := SystemPrompt()
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}

	if !strings.Contains(got, thesis.Statement) {
		t.Error("thesis statement missing from the system prompt")
	}
	for _, c := range thesis.Criteria {
		if !strings.Contains(got, c.Key) {
			t.Errorf("criterion %q missing from the system prompt", c.Key)
		}
		if !strings.Contains(got, c.Zero) || !strings.Contains(got, c.Five) {
			t.Errorf("criterion %q is missing its scale anchors", c.Key)
		}
	}
	if !strings.Contains(got, AnswerToolName) {
		t.Error("the answer tool is never named, so the model has no instruction to call it")
	}
}

func TestUserPromptReflectsFetchOutcome(t *testing.T) {
	t.Run("successful fetch inlines the page and suppresses re-fetching", func(t *testing.T) {
		page := okPage()
		page.Text = "We close your books automatically and file your returns."

		got, err := UserPrompt(testCandidate(), page)
		if err != nil {
			t.Fatalf("UserPrompt: %v", err)
		}

		if !strings.Contains(got, page.Text) {
			t.Error("page text was not inlined; the model would pay to re-fetch it")
		}
		if !strings.Contains(got, "do not need to fetch this page again") {
			t.Error("prompt does not tell the model the page is already retrieved")
		}
	})

	t.Run("failed fetch states the reason and asks for escalation", func(t *testing.T) {
		got, err := UserPrompt(testCandidate(), fetch.Result{
			URL: "https://ledgerly.com", Outcome: fetch.OutcomeBlocked, Detail: "http 403",
		})
		if err != nil {
			t.Fatalf("UserPrompt: %v", err)
		}

		if !strings.Contains(got, "blocked") || !strings.Contains(got, "http 403") {
			t.Errorf("escalation reason missing from prompt:\n%s", got)
		}
		if !strings.Contains(got, "web fetch") {
			t.Error("prompt does not ask the model to fetch the page itself")
		}
	})

	t.Run("signals and their URLs are always included", func(t *testing.T) {
		got, err := UserPrompt(testCandidate(), okPage())
		if err != nil {
			t.Fatalf("UserPrompt: %v", err)
		}

		if !strings.Contains(got, "https://news.ycombinator.com/item?id=1") {
			t.Error("signal URL missing; the model can't check the traction claim")
		}
	})
}

// The tool schema is what makes the model's output typed rather than parsed prose, so
// the two must stay in step: every field the schema requires has to decode into the
// payload struct.
func TestAnswerToolSchemaMatchesPayload(t *testing.T) {
	tool := AnswerTool()

	if tool.Name != AnswerToolName {
		t.Errorf("tool name = %q, want %q", tool.Name, AnswerToolName)
	}
	if !tool.Strict.Valid() || !tool.Strict.Value {
		t.Error("tool is not strict; inputs would need defensive parsing")
	}

	props, ok := tool.InputSchema.Properties.(map[string]any)
	if !ok {
		t.Fatalf("Properties is %T, want map[string]any", tool.InputSchema.Properties)
	}

	// Every required field must be a declared property.
	for _, req := range tool.InputSchema.Required {
		if _, exists := props[req]; !exists {
			t.Errorf("field %q is required but not declared as a property", req)
		}
	}

	// Round-trip a payload shaped like the schema to confirm the tags line up.
	blob, err := json.Marshal(answerPayload{Criteria: gradeAll(3)})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var back answerPayload
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("payload does not round-trip: %v", err)
	}
	if len(back.Criteria) != len(thesis.Criteria) {
		t.Errorf("criteria count changed across round trip: %d", len(back.Criteria))
	}

	// The criterion key enum must list exactly the thesis criteria, or the model can
	// return a key the scorer discards.
	criteria, _ := props["criteria"].(map[string]any)
	items, _ := criteria["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	keyProp, _ := itemProps["key"].(map[string]any)
	enum, _ := keyProp["enum"].([]string)

	if len(enum) != len(thesis.Criteria) {
		t.Fatalf("key enum has %d entries, want %d", len(enum), len(thesis.Criteria))
	}
	for i, c := range thesis.Criteria {
		if enum[i] != c.Key {
			t.Errorf("enum[%d] = %q, want %q", i, enum[i], c.Key)
		}
	}
}
