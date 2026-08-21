package analyze

import (
	"bytes"
	"fmt"
	"text/template"
	"time"

	"github.com/Soumik43/emergence/internal/fetch"
	"github.com/Soumik43/emergence/internal/model"
	"github.com/Soumik43/emergence/internal/thesis"
	"github.com/Soumik43/emergence/prompts"
)

// Parsed at init so a broken template fails the binary immediately rather than on the
// first candidate, halfway through a paid run.
var (
	systemTmpl = template.Must(template.ParseFS(prompts.FS, "analyst_system.md.tmpl"))
	userTmpl   = template.Must(template.ParseFS(prompts.FS, "analyst_user.md.tmpl"))
)

// systemView is the data the system prompt renders from. It is derived entirely from
// internal/thesis, so the rubric the model grades against cannot drift from the rubric
// the scorer computes with — there is only one copy.
type systemView struct {
	Statement  string
	Criteria   []thesis.Criterion
	AnswerTool string
}

// SystemPrompt renders the analyst instructions. Byte-identical for every candidate in a
// run, which is what makes it worth prompt-caching.
func SystemPrompt() (string, error) {
	var buf bytes.Buffer
	err := systemTmpl.Execute(&buf, systemView{
		Statement:  thesis.Statement,
		Criteria:   thesis.Criteria,
		AnswerTool: AnswerToolName,
	})
	if err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}
	return buf.String(), nil
}

// userView is the per-candidate prompt data.
type userView struct {
	Name          string
	Website       string
	OneLiner      string
	Founders      []string
	Signals       []signalView
	PageURL       string
	PageTitle     string
	PageText      string
	FetchOutcome  string
	FetchDetail   string
	CriteriaCount int
	AnswerTool    string
	Today         string
}

type signalView struct {
	Kind   string
	Detail string
	Date   string
	URL    string
}

// UserPrompt renders the per-candidate prompt, including tier-1 page text when we have
// it. When tier 1 failed, the prompt says so explicitly and hands the model the URL to
// fetch itself — the escalation is stated in the prompt rather than hidden, so the model
// knows the difference between "no page" and "page not yet read".
func UserPrompt(c model.Candidate, page fetch.Result) (string, error) {
	view := userView{
		Name:          c.Name,
		Website:       c.Website,
		OneLiner:      c.OneLiner,
		Founders:      c.Founders,
		CriteriaCount: len(thesis.Criteria),
		AnswerTool:    AnswerToolName,
		Today:         time.Now().Format("2 January 2006"),
		PageURL:       page.URL,
		PageTitle:     page.Title,
		FetchOutcome:  string(page.Outcome),
		FetchDetail:   page.Detail,
	}
	if page.Outcome == fetch.OutcomeOK {
		view.PageText = page.Text
	}
	if view.FetchDetail == "" {
		view.FetchDetail = "no detail recorded"
	}

	for _, s := range c.Signals {
		view.Signals = append(view.Signals, signalView{
			Kind:   string(s.Kind),
			Detail: s.Detail,
			Date:   s.Date.Format("2 Jan 2006"),
			URL:    s.URL,
		})
	}

	var buf bytes.Buffer
	if err := userTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render user prompt for %s: %w", c.ID, err)
	}
	return buf.String(), nil
}
