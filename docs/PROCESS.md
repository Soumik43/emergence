# Process

> ## ⚠️ SOUMIK — THIS FILE IS THE ONE YOU HAVE TO WRITE YOURSELF
>
> Claude scaffolded this. The prompts and the factual timeline below are real and are
> yours to use, but **every section marked `TODO` needs your own words.**
>
> The brief is explicit: *"Don't ghostwrite reflective writing. It's obvious."* and lists
> *"Reflective writing that reads like a model wrote it about how a model was used"* as an
> anti-pattern. Process visibility is 40% of the grade. A model-written reflection about
> working with a model is the single fastest way to lose those points — worse than leaving
> this file out entirely, because it reads as an attempt to fake the thing being graded.
>
> Delete this banner when you've filled it in.

---

## What this is

A record of how the pipeline in this repo actually got built, over one session, working
with Claude Code.

For who-wrote-what, see [AI_ATTRIBUTION.md](AI_ATTRIBUTION.md) — that's the factual
ledger, and it's already complete. This file is for the part a ledger can't carry: what I
was thinking, where I was wrong, and what I'd do differently.

---

## How I framed the problem before writing anything

**TODO — your words.** Things worth answering, from what you actually said at the start:

- You read the brief and asked *what do I actually need to do here* before touching code.
  Why? What was ambiguous?
- The two questions you raised unprompted were **"we'll have to bypass Cloudflare"** and
  **"how do we search properly when we aren't an LLM ourselves?"** Both turned out to have
  cheaper answers than expected. What made you reach for the hard version first — and is
  that a habit worth keeping, or the thing to correct?
- You specified Go and Cobra, and asked for a commit per working stage so it would be
  revertable. Why that, on a throwaway take-home?

---

## Where I overrode the model, and why

**TODO — your words.** This is the most valuable section in the file; don't rush it.

The factual record: Claude's first design had **no local fetching at all** — every page
read through Anthropic's server-side `web_fetch`. You rejected it mid-build:

> *"its not necessary that we want to go through a no scraping stack, we would rather go
> with a hybrid model for the both, and when needed we wisely use what we want to"*

That became [ADR 002](decisions/002-hybrid-fetch-ladder.md) and the three-tier ladder in
`internal/fetch`. The diagnosis written into that ADR is that Claude had conflated "avoid
a scraping stack" with "never fetch locally" — two different choices — and had taken a
defensible principle one step too far into a slogan.

Worth writing down:

- What made you push back? Cost, control, or something about the shape of the design?
- Did you spot it because you know what `http.Get` costs, or because "never do X" as an
  architecture smelled wrong?
- The model produced a *confident, well-argued ADR* for the design you then rejected. What
  does that tell you about reviewing agent output? Where else in this repo might the same
  thing have happened and not been caught?

---

## What I let the model decide, and whether that was right

**TODO — your words.**

[AI_ATTRIBUTION.md](AI_ATTRIBUTION.md#what-claude-decided-without-review) lists what went
in without a second pair of eyes: the source choice, the six criteria and their weights,
missing-evidence scoring 1/5 rather than the midpoint, every threshold, the relevance
screen, what got tested.

The weights are the interesting one — they're the load-bearing claim of the whole scoring
system (`founder_workflow_proximity` at 25 vs `traction_signal` at 10), they're argued for
in THESIS.md, and they're validated against nothing at all.

- Which of those would you actually want to review before this ran on real deal flow?
- Which are fine to leave to the model, and what distinguishes the two groups?

---

## The bug that no test would have caught

**TODO — your words.**

Stage 1 was built, unit-tested, and green. The first live run against
`--query "AI agents for SMBs"` returned twelve real companies, every one of them developer
tooling for people *building* agents — MCP gateways, agent observability, email infra for
agents. Zero were software sold to a small business. The code was working exactly as
written; HN's search is keyword-based and "for SMBs" contributed nothing to the match.

That produced [ADR 004](decisions/004-relevance-screen.md) and `internal/screen`.

- What does it say about the tests that they were all green?
- Green tests, plausible code, wrong output — how do you catch that class of problem
  earlier next time?

---

## What I'd do differently

**TODO — your words.** Be specific and be willing to name something that isn't finished.
Honest candidates, if you agree with them:

- The scoring rubric has never been calibrated against a company whose outcome is known.
  There's no way to tell whether a 72 means anything.
- Only one source shipped. `source.Source` is an interface with a single implementation,
  which is speculative generality until the second one exists.
- The relevance screen was reactive — bolted on after a bad result rather than designed in.
- `--no-screen --force` is the escape hatch for a wrongly-screened company, but nothing
  makes a wrong screen *visible* apart from reading the index.

---

## If I ran this again

**TODO — your words.** What would you change about how you worked with the agent, not
about the code?

---

## Timeline

Factual, for reference while writing the above. `git log --reverse --format="%h %s"` has
the rest.

| # | Commit | What landed |
|---|---|---|
| 1 | Thesis and ADRs | Thesis written *before* any code, so the rubric had something to be accountable to |
| 2 | Types + scoring | Scorer with tests for malformed model output |
| 3 | Tier-1 fetcher | Hybrid ladder after your override; three bugs caught by tests |
| 4 | Stage 1 sourcing | HN + quality filter; double-counting bug caught |
| 5 | Stage 2 analysis | Strict tool call, wire-level tracing |
| 6 | Stage 3 memos | Deterministic rendering, no model calls |
| 7 | Relevance screen | Response to the bad live run |
