# emergence

An investment-triage pipeline. Point it at a topic, get one memo per startup ending in
**Pass / Watch / Take a meeting**, with every claim traceable to a URL.

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go build -o bin/emergence ./cmd/emergence
./bin/emergence run --query "AI agents for SMBs"
```

Output lands in `runs/<topic>-<date>/`. Start with `index.md`.

**Committed runs are in [`runs/`](runs/)** — you don't need to run anything or spend
tokens to read the output.

## What it does

Three stages, each writing its output to disk before the next one reads it.

| Stage | Command | Does | Costs |
|---|---|---|---|
| 1. Source | `emergence source` | Searches Hacker News, filters to real companies, screens out off-segment ones | One cheap model call |
| 2. Analyse | `emergence analyze` | Researches each company and grades it against the thesis | The expensive part |
| 3. Memo | `emergence memo` | Renders memos and the run index | Free — no model calls |

`emergence run` does all three. Every stage skips work already on disk unless `--force`,
so an interrupted run resumes instead of re-paying, and the memo format can be iterated on
for free.

```
runs/ai-agents-for-smbs-2026-08-21/
├── index.md            ← ranked table of every call. Start here.
├── manifest.json       what was asked for, what it cost, what failed
├── candidates.json     stage 1 output
├── analysis/*.json     stage 2 output, one file per company
├── memos/*.md          stage 3 output — the one-pagers
└── trace/*.json        every model exchange, verbatim
```

## The thesis

Scores are meaningless without one, so this fund has a specific one:

> Execution-layer AI that completes a recurring, measurable back-office workflow for SMBs
> (10–500 employees), built by founders who have lived that workflow, where every completed
> execution makes the next one better.

Six weighted criteria, 0–5 each, weighted to 100. `emergence thesis` prints the rubric;
[docs/THESIS.md](docs/THESIS.md) argues for it, including what would make me abandon it.

Developer tooling, infra, and consumer are *deliberately* out of scope and score low. That
is the rubric working, not a bug.

## How to check whether it's lying to you

The pipeline is designed to be spot-checked, because "the model said 73" is not a
defensible score.

1. **Open any memo.** The call, the score and the reason are above the first horizontal
   rule — that's the 60-second read. Everything below it is the audit trail.
2. **Pick a claim.** Every claim carries a `[source]` link. Claims the model asserted
   without a URL are rendered as *unsourced* rather than quietly presented as facts.
3. **Check the arithmetic.** The score table decomposes into six criteria × their weights.
   The model graded the criteria; `internal/thesis/score.go` did the sum. Disagree with a
   62 by finding the 2 that produced it.
4. **Read the trace.** `trace/<company>.json` holds every request and response verbatim,
   including each web search the model ran and each page it read. That is where a claim's
   provenance actually lives.

## Design decisions

The reasoning behind the five choices that shaped this, including what each one cost:

- [001 — Two sources, not twelve](docs/decisions/001-two-sources-not-twelve.md)
- [002 — Hybrid fetch: cheap local first, server-side on escalation](docs/decisions/002-hybrid-fetch-ladder.md)
- [003 — The model grades criteria; Go computes the score](docs/decisions/003-score-in-code.md)
- [004 — A cheap relevance screen between sourcing and analysis](docs/decisions/004-relevance-screen.md)
- [005 — Decompose the topic; a partner's phrasing is not a search query](docs/decisions/005-query-expansion.md)

How this was built, and where Claude did the work:
[docs/PROCESS.md](docs/PROCESS.md) · [docs/AI_ATTRIBUTION.md](docs/AI_ATTRIBUTION.md)

## Handling bad data

Missing and broken data is the *normal* case here, not the edge case, so it's designed for
rather than guarded against:

- **A criterion with no evidence scores 1/5, not the midpoint**, and lowers the memo's
  `confidence`. A low-confidence Pass is labelled "on current evidence", which is a
  different statement from Pass.
- **An unsourced grade is discarded.** `assemble()` drops evidence with no URL and
  `thesis.Score` treats an empty evidence list as absent — so a 5 asserted on nothing
  scores as a 1.
- **A blocked or client-rendered site escalates** from the local fetcher to server-side
  fetch, with the reason recorded. A genuinely dead site is reported as a finding.
- **One failed company doesn't sink the run.** Failures are recorded and listed in the
  index, so a run with 14 of 18 memos explains the missing four.

## Layout

```
cmd/emergence/        entry point
internal/
  thesis/             the rubric and the scoring maths — the single source of truth
  source/             stage 1: Hacker News
  screen/             stage 1: relevance screen
  fetch/              tier-1 local fetcher with typed escalation reasons
  llm/                Anthropic client, tool loop, wire-level tracing
  analyze/            stage 2
  memo/               stage 3
  run/                the run directory: the boundary between stages
  model/              the shapes stages pass to each other
prompts/              prompt templates, at the root so they're easy to find and diff
docs/                 thesis, decisions, process
```

## Flags worth knowing

| Flag | Why |
|---|---|
| `--only <id>` | Analyse one company. Debug a memo without paying for nineteen others. |
| `--force` | Redo cached work. |
| `--no-screen` | Skip the relevance screen; stage 1 becomes free and deterministic. |
| `--no-local-fetch` | Read every page server-side. Isolates whether a bad memo came from thin local extraction or from the model. |
| `--concurrency N` | Parallel analyses. Default 3. |
| `--limit N` | Candidates to carry forward. Default 15. |

## Requirements

Go 1.24+ and an `ANTHROPIC_API_KEY`. Hacker News needs no key. `make test` runs the suite,
which needs neither.
