# 003 — The model grades criteria; Go computes the score

**Status:** accepted · **Stage:** analysis

## Context

The obvious implementation is to hand Claude the thesis and ask for a number 0–100. It
works, and it produces a defensible-*sounding* score. The rubric asks for scores that are
actually defensible, and "the model said 73" is not defensible — I can't tell you why it
wasn't 68, and it won't say the same thing twice.

## Decision

Split the judgement from the arithmetic:

1. **The model does what only it can do** — read the evidence and grade each of the six
   thesis criteria `0–5`, with a one-line justification and the source URLs that support
   the grade. This is emitted through a strict-schema tool call, so it's typed, not prose
   I have to regex.
2. **Go does the arithmetic** — `internal/thesis/score.go` applies the weights, computes the
   0–100 total, derives `confidence` from evidence coverage, and maps the band to
   Pass/Watch/Take a meeting.

## Why it matters

- **Auditable.** Every score decomposes into six graded criteria × a weight in
  [THESIS.md](../THESIS.md). A partner who disagrees with a 62 can see it came from a 2 on
  `data_loop`, read the justification and its source URL, and disagree with *that*.
- **The weights become a knob, not a rewrite.** Re-weighting the thesis is an edit to one
  table and a re-run of stage 3 (free, no LLM calls) — not a re-analysis.
- **No drift.** The band boundary is a constant in code, so the same criterion grades always
  yield the same call. The model's non-determinism is confined to grading, where it's
  unavoidable.
- **The failure mode gets cheaper.** A model asked for one number will happily return 100
  or 0 on garbage input. A model asked to grade six criteria against absent evidence
  returns low grades with "no evidence found," which the confidence calculation can see.

## Consequence I had to accept

The model can no longer express "this doesn't fit the rubric but I have a strong feeling."
That is mostly the point, but it does mean an unusual company that breaks the rubric's
assumptions gets flattened. Partially mitigated: the analysis schema has a free-text
`analyst_note` field that bypasses scoring and is rendered verbatim at the end of the memo,
so an off-rubric observation survives into the output instead of being discarded.
