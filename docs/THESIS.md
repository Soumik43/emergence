# Investment Thesis

Everything in this pipeline scores against this one thesis. If you disagree with the
thesis, you should disagree with the scores — that is the point. A thesis you can't
disagree with isn't a thesis.

## The thesis, in one paragraph

**We back execution-layer AI that completes a recurring, measurable back-office workflow
for small and mid-sized businesses (roughly 10–500 employees), where the founding team has
lived that workflow, and where every completed execution makes the next one better.**

Deliberately excluded: horizontal copilots, developer tooling, model/infra layers,
consumer, and anything selling to enterprises with a >6-month procurement cycle. Those may
be excellent businesses. They are not this fund's edge, so they score low here and that is
correct behaviour, not a bug.

## Why this thesis

Three claims, each of which could be wrong:

1. **Suggestion is a feature; execution is a company.** A copilot that drafts a reply
   competes with whatever incumbent already owns the inbox, and loses on distribution. An
   agent that *sends* the reply, *files* the return, *chases* the invoice owns an outcome
   the incumbent can't retrofit without rebuilding their liability model.
2. **SMBs are where agents get to actually act.** SMBs have no procurement committee, no
   security review, and no internal team to build it themselves. They will grant write
   access for a clear ROI. Enterprises will not, yet. So the SMB market is where
   execution-layer agents can be *tested against reality* in 2026, not 2029.
3. **Workflow proximity beats pedigree at seed.** For a narrow back-office workflow, a
   founder who has personally done the job knows the twelve exceptions that decide whether
   the agent can be trusted to act. A stronger-on-paper team without that exposure
   discovers the exceptions in production, in front of the first ten customers.

## Scoring rubric

Six criteria. Each is scored **0–5** by the analysis model against gathered evidence,
then the total is computed **in Go, not by the model** (`internal/thesis/score.go`), so
the arithmetic is auditable and reproducible.

| Criterion | Weight | 0 | 5 |
|---|---:|---|---|
| `founder_workflow_proximity` | 25 | No connection to the workflow being automated | Founder personally did this job, or ran the function being sold to |
| `execution_depth` | 20 | Suggests, drafts, summarises; a human still acts | Takes the terminal action end-to-end with a defined liability story |
| `data_loop` | 20 | Stateless wrapper; nothing compounds | Each execution produces proprietary correction/outcome data that measurably improves the next |
| `technical_depth` | 15 | Prompt-and-Zapier; no evidence of engineering | Non-trivial systems work: eval harness, deterministic fallbacks, integration depth |
| `wedge_and_why_now` | 10 | "AI for business" — unfalsifiable in 12 months | One workflow, one buyer, one metric; and a concrete reason this became possible recently |
| `traction_signal` | 10 | No signal, or vanity signal only | Named paying customers, revenue, or retention past the novelty window |

**Weights are a claim about what predicts outcomes at seed**, in this segment. Founder
proximity is weighted highest because it is the hardest thing to fix after the fact.
Traction is weighted lowest because at seed there usually isn't any, and rewarding it
mostly rewards being three months older.

## Bands → call

| Weighted score | Call |
|---:|---|
| 70–100 | **Take a meeting** |
| 45–69 | **Watch** |
| 0–44 | **Pass** |

## Missing evidence is not neutral

If the pipeline cannot find evidence for a criterion, that criterion scores **1, not 2.5**,
and the memo says so explicitly.

This is a deliberate bias toward Pass. A seed fund's default answer is no, and an absent
signal is weak evidence of an absent thing — a founder who has lived the workflow usually
says so on their own landing page. The cost of the bias is real (a quiet team gets
under-scored) so the pipeline surfaces it rather than hiding it: every memo carries a
`confidence` value derived from evidence coverage, and a low-confidence Pass is explicitly
labelled as "pass on current evidence", which is a different statement from "pass".

## What would make me change this thesis

Held honestly, a thesis has failure conditions:

- If SMBs turn out to churn out of execution-layer agents at >5%/month once novelty wears
  off, then "SMBs will grant write access" is wrong and the segment is a treadmill.
- If a horizontal incumbent (a Shopify, an Intuit) ships a good-enough execution agent as a
  bundled feature and it holds, then the wedge thesis collapses into distribution and seed
  entry gets much worse.
- If eval tooling commoditises workflow-specific reliability, `technical_depth` and
  `data_loop` stop discriminating and this rubric mostly measures noise.
