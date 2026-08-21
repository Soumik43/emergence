package thesis

import (
	"testing"

	"github.com/Soumik43/emergence/internal/model"
)

// ev builds a graded criterion that counts as evidence-backed.
func ev(key string, grade int) model.CriterionGrade {
	return model.CriterionGrade{
		Key:           key,
		Grade:         grade,
		Justification: "because",
		Evidence:      []model.Evidence{{Claim: "c", URL: "https://example.com"}},
	}
}

// allGraded grades every thesis criterion at the same level, with evidence.
func allGraded(grade int) []model.CriterionGrade {
	out := make([]model.CriterionGrade, 0, len(Criteria))
	for _, c := range Criteria {
		out = append(out, ev(c.Key, grade))
	}
	return out
}

// The weights are a claim about what predicts outcomes; if they stop summing to 100
// the score silently stops being a percentage.
func TestWeightsSumTo100(t *testing.T) {
	if got := totalWeight(); got != 100 {
		t.Fatalf("thesis weights sum to %d, want 100", got)
	}
}

func TestCriteriaKeysUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Criteria {
		if seen[c.Key] {
			t.Fatalf("duplicate criterion key %q", c.Key)
		}
		seen[c.Key] = true
	}
}

func TestScoreEndsOfScale(t *testing.T) {
	tests := []struct {
		name      string
		grade     int
		wantTotal int
		wantCall  model.Call
		wantConf  float64
	}{
		{"perfect", MaxGrade, 100, model.CallMeeting, 1},
		{"floor", 0, 0, model.CallPass, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(allGraded(tc.grade))
			if got.Total != tc.wantTotal {
				t.Errorf("Total = %d, want %d", got.Total, tc.wantTotal)
			}
			if got.Call != tc.wantCall {
				t.Errorf("Call = %q, want %q", got.Call, tc.wantCall)
			}
			if got.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tc.wantConf)
			}
			if got.Provisional {
				t.Error("Provisional = true, want false with full evidence coverage")
			}
		})
	}
}

// A criterion the model never returned must still appear in the breakdown, scored as
// absent. If it silently vanished, a model could raise a score by omitting its weakest
// dimension.
func TestScoreImputesMissingCriteria(t *testing.T) {
	only := []model.CriterionGrade{ev(Criteria[0].Key, MaxGrade)}

	got := Score(only)

	if len(got.Breakdown) != len(Criteria) {
		t.Fatalf("breakdown has %d rows, want %d (one per criterion)", len(got.Breakdown), len(Criteria))
	}
	for _, row := range got.Breakdown[1:] {
		if !row.Imputed {
			t.Errorf("%s: Imputed = false, want true", row.Key)
		}
		if row.Grade != NoEvidenceGrade {
			t.Errorf("%s: Grade = %d, want NoEvidenceGrade %d", row.Key, row.Grade, NoEvidenceGrade)
		}
	}

	// Confidence is weight-aware: only criterion[0]'s weight was evidenced.
	wantConf := float64(Criteria[0].Weight) / 100
	if got.Confidence != wantConf {
		t.Errorf("Confidence = %v, want %v", got.Confidence, wantConf)
	}
	if !got.Provisional {
		t.Error("Provisional = false, want true when most weight lacks evidence")
	}
}

// A grade with no supporting URL is an unsourced assertion. It must not earn
// confidence, and it must not keep its grade — otherwise the model can score a company
// highly on nothing at all.
func TestScoreTreatsUnsourcedGradeAsAbsent(t *testing.T) {
	unsourced := make([]model.CriterionGrade, 0, len(Criteria))
	for _, c := range Criteria {
		unsourced = append(unsourced, model.CriterionGrade{
			Key: c.Key, Grade: MaxGrade, Justification: "trust me",
		})
	}

	got := Score(unsourced)

	if got.Confidence != 0 {
		t.Errorf("Confidence = %v, want 0 when no criterion cites a source", got.Confidence)
	}
	// Every criterion falls back to NoEvidenceGrade: 1/5 of every weight = 20.
	if got.Total != 20 {
		t.Errorf("Total = %d, want 20 (NoEvidenceGrade across all weights)", got.Total)
	}
	if got.Call != model.CallPass {
		t.Errorf("Call = %q, want Pass", got.Call)
	}
}

// NoEvidence is the model's own admission that it found nothing; honour it even when
// it also returned a grade and a URL.
func TestScoreHonoursNoEvidenceFlag(t *testing.T) {
	graded := allGraded(MaxGrade)
	graded[0].NoEvidence = true

	got := Score(graded)

	if !got.Breakdown[0].Imputed {
		t.Error("criterion flagged NoEvidence was not treated as imputed")
	}
	if got.Breakdown[0].Grade != NoEvidenceGrade {
		t.Errorf("Grade = %d, want %d", got.Breakdown[0].Grade, NoEvidenceGrade)
	}
}

func TestScoreIsRobustToMalformedInput(t *testing.T) {
	t.Run("nil input scores the floor, not a panic", func(t *testing.T) {
		got := Score(nil)
		if got.Total != 20 || got.Call != model.CallPass {
			t.Errorf("Score(nil) = %d/%q, want 20/Pass", got.Total, got.Call)
		}
		if !got.Provisional {
			t.Error("Score(nil).Provisional = false, want true")
		}
	})

	t.Run("out-of-range grades are clamped", func(t *testing.T) {
		graded := allGraded(MaxGrade)
		graded[0].Grade = 99
		graded[1].Grade = -7

		got := Score(graded)

		if got.Breakdown[0].Grade != MaxGrade {
			t.Errorf("grade 99 clamped to %d, want %d", got.Breakdown[0].Grade, MaxGrade)
		}
		if got.Breakdown[1].Grade != 0 {
			t.Errorf("grade -7 clamped to %d, want 0", got.Breakdown[1].Grade)
		}
		if got.Total < 0 || got.Total > 100 {
			t.Errorf("Total = %d, want within 0..100", got.Total)
		}
	})

	t.Run("unknown criterion keys are ignored", func(t *testing.T) {
		graded := append(allGraded(MaxGrade), ev("vibes", MaxGrade))

		got := Score(graded)

		if got.Total != 100 {
			t.Errorf("Total = %d, want 100 — an off-thesis key must not add points", got.Total)
		}
		if len(got.Breakdown) != len(Criteria) {
			t.Errorf("breakdown grew to %d rows; unknown key leaked in", len(got.Breakdown))
		}
	})
}

func TestCallBands(t *testing.T) {
	tests := []struct {
		total int
		want  string
	}{
		{100, "Take a meeting"},
		{70, "Take a meeting"},
		{69, "Watch"},
		{45, "Watch"},
		{44, "Pass"},
		{0, "Pass"},
	}
	for _, tc := range tests {
		if got := callFor(tc.total); got != tc.want {
			t.Errorf("callFor(%d) = %q, want %q", tc.total, got, tc.want)
		}
	}
}
