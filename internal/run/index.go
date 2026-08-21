package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Soumik43/emergence/internal/model"
)

// WriteIndex writes the run's index.md: one ranked table of every memo.
//
// This is the file a partner opens first, and for most candidates it is the only file
// they open — the whole point of the pipeline is deciding which 10% earn a memo read. So
// it leads with the calls, and it states what the run failed to do rather than quietly
// being short a few rows.
func (r *Run) WriteIndex(analyses []model.Analysis) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", r.Manifest.Query)
	fmt.Fprintf(&b, "%d candidates sourced from %s · %d analysed",
		r.Manifest.Candidates, r.Manifest.Source, len(analyses))
	if r.Manifest.Failed > 0 {
		fmt.Fprintf(&b, " · %d failed", r.Manifest.Failed)
	}
	fmt.Fprintf(&b, " · %s\n\n", r.Manifest.UpdatedAt.Format("2 Jan 2006 15:04 MST"))
	fmt.Fprintf(&b, "> **Thesis.** %s\n\n", r.Manifest.Thesis)

	// Group by call. A partner reads the meetings, skims the watches, and trusts the
	// passes — so the shortlist has to be visible without scanning a score column.
	buckets := []struct {
		call  model.Call
		blurb string
	}{
		{model.CallMeeting, "Worth an hour."},
		{model.CallWatch, "Not yet, but track them."},
		{model.CallPass, "No, and here is why."},
	}

	for _, bucket := range buckets {
		rows := filterCall(analyses, bucket.call)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s — %d\n\n%s\n\n", bucket.call, len(rows), bucket.blurb)
		b.WriteString("| Score | Conf. | Company | What they do | Why this call |\n")
		b.WriteString("|---:|---:|---|---|---|\n")
		for _, a := range rows {
			fmt.Fprintf(&b, "| **%d** | %d%% | [%s](memos/%s.md) | %s | %s |\n",
				a.Score.Total,
				int(a.Score.Confidence*100+0.5),
				cell(a.Name),
				a.CandidateID,
				cell(oneLine(a)),
				cell(headline(a)),
			)
		}
		b.WriteString("\n")
	}

	if len(r.Manifest.Screened) > 0 {
		fmt.Fprintf(&b, "## Screened out before analysis — %d\n\n", len(r.Manifest.Screened))
		b.WriteString("Hacker News search is keyword-based, so a topic query returns companies " +
			"that share vocabulary with it but not its market. These were dropped on their own " +
			"one-line description, before paying to analyse them. Listed so the run's coverage " +
			"is checkable rather than assumed — re-run with `--no-screen --force` to analyse " +
			"them anyway.\n\n")
		b.WriteString("| Company | Screened out because |\n|---|---|\n")
		for _, s := range r.Manifest.Screened {
			fmt.Fprintf(&b, "| %s | %s |\n", cell(s.Name), cell(s.Reason))
		}
		b.WriteString("\n")
	}

	if len(r.Manifest.Failures) > 0 {
		b.WriteString("## Not analysed\n\n")
		b.WriteString("These candidates were sourced but produced no memo. Listed rather than " +
			"dropped, so the run's coverage is honest.\n\n")
		for _, line := range sortedFailures(r.Manifest.Failures) {
			b.WriteString("- " + line + "\n")
		}
		b.WriteString("\n")
	}

	c := r.Manifest.Cost
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "*Run cost: %s input tokens, %s output tokens, %d web searches, "+
		"%d server-side fetches. Local fetcher handled %d of %d company sites; %d escalated.*\n",
		thousands(c.InputTokens), thousands(c.OutputTokens), c.WebSearches, c.WebFetches,
		c.LocalFetchOK, c.LocalFetchOK+c.Escalated, c.Escalated)

	return os.WriteFile(filepath.Join(r.Dir, indexFile), []byte(b.String()), 0o644)
}

func filterCall(analyses []model.Analysis, call model.Call) []model.Analysis {
	var out []model.Analysis
	for _, a := range analyses {
		if a.Score.Call == call {
			out = append(out, a)
		}
	}
	return out
}

// headline is the one-clause reason for the call: the weakest criterion for a pass, the
// strongest for a meeting. It is what makes the index scannable rather than just a
// leaderboard.
func headline(a model.Analysis) string {
	if len(a.Score.Breakdown) == 0 {
		return "Not graded."
	}

	best, worst := a.Score.Breakdown[0], a.Score.Breakdown[0]
	for _, row := range a.Score.Breakdown {
		if row.Grade > best.Grade {
			best = row
		}
		if row.Grade < worst.Grade {
			worst = row
		}
	}

	if a.Score.Provisional {
		return fmt.Sprintf("Thin evidence — %d%% of the score rests on what could not be found.",
			100-int(a.Score.Confidence*100+0.5))
	}

	switch a.Score.Call {
	case model.CallMeeting:
		return fmt.Sprintf("Strong on %s (%d/5).", strings.ToLower(best.Label), best.Grade)
	default:
		return fmt.Sprintf("Weak on %s (%d/5).", strings.ToLower(worst.Label), worst.Grade)
	}
}

func oneLine(a model.Analysis) string {
	s := strings.TrimSpace(a.Product.Summary)
	if s == "" {
		return "—"
	}
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	// Keep table rows to one line on a normal terminal.
	if len(s) > 90 {
		s = strings.TrimSpace(s[:87]) + "…"
	}
	return s
}

// cell escapes the characters that would break a markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// sortedFailures returns failure lines in candidate-ID order.
//
// A slice, not a map: the index is a committed file, and iterating a map here would
// reshuffle these lines on every run and produce noise diffs on output that hadn't
// changed.
func sortedFailures(failures map[string]string) []string {
	ids := make([]string, 0, len(failures))
	for id := range failures {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("**%s** — %s", id, cell(failures[id])))
	}
	return out
}

// thousands formats an int with comma separators.
func thousands(n int) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}
