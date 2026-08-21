package thesis

import (
	"math"
	"sort"

	"github.com/Soumik43/emergence/internal/model"
)

// Score turns the model's per-criterion grades into a weighted total and a call.
//
// This is deliberately the only place the arithmetic happens, and the model never sees
// it: the model grades criteria, Go computes the score. See
// docs/decisions/003-score-in-code.md.
//
// It is total about bad input, because bad input is the normal case here — a model may
// omit a criterion, invent a key, grade 9 out of 5, or return nothing at all. Every one
// of those degrades the score and the confidence rather than failing the run.
func Score(graded []model.CriterionGrade) model.Score {
	byKey := make(map[string]model.CriterionGrade, len(graded))
	for _, g := range graded {
		// Last write wins on duplicate keys; unknown keys are dropped, since a
		// criterion outside the thesis has no weight to contribute.
		if _, known := ByKey(g.Key); known {
			byKey[g.Key] = g
		}
	}

	var (
		points         float64
		evidenceWeight int
		breakdown      = make([]model.ScoredCriterion, 0, len(Criteria))
	)

	// Iterate Criteria, not the input: a criterion the model never returned still
	// has to appear in the breakdown, scored as absent. Otherwise a model that
	// silently drops its weakest criterion would score higher for it.
	for _, c := range Criteria {
		g, present := byKey[c.Key]

		grade := g.Grade
		imputed := !present || g.NoEvidence || len(g.Evidence) == 0
		if imputed {
			grade = NoEvidenceGrade
		}
		grade = clamp(grade, 0, MaxGrade)

		if !imputed {
			evidenceWeight += c.Weight
		}

		p := float64(grade) / float64(MaxGrade) * float64(c.Weight)
		points += p

		breakdown = append(breakdown, model.ScoredCriterion{
			Key:     c.Key,
			Label:   c.Label,
			Grade:   grade,
			Weight:  c.Weight,
			Points:  round1(p),
			Imputed: imputed,
		})
	}

	total := clamp(int(math.Round(points)), 0, 100)
	confidence := float64(evidenceWeight) / float64(totalWeight())

	return model.Score{
		Total:       total,
		Call:        model.Call(callFor(total)),
		Confidence:  round2(confidence),
		Breakdown:   breakdown,
		Provisional: confidence < ProvisionalBelow,
	}
}

// callFor maps a total to a band. Bands are sorted descending so the first match wins.
func callFor(total int) string {
	bands := append([]struct {
		Min  int
		Call string
	}(nil), Bands...)
	sort.Slice(bands, func(i, j int) bool { return bands[i].Min > bands[j].Min })

	for _, b := range bands {
		if total >= b.Min {
			return b.Call
		}
	}
	return "Pass" // unreachable while a 0-floor band exists; kept so the func is total
}

func totalWeight() int {
	sum := 0
	for _, c := range Criteria {
		sum += c.Weight
	}
	return sum
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func round2(f float64) float64 { return math.Round(f*100) / 100 }
