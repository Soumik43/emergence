# 004 — A cheap relevance screen between sourcing and analysis

**Status:** accepted · **Stage:** sourcing

## Context

The first live run of stage 1 against `--query "AI agents for SMBs"` returned twelve real
companies. The domain filter worked — no Substack posts, no arXiv papers, no GitHub
repos. But the *results* were:

```
Sprocket        AI agent for hardware and software development
Ama2            messenger built for AI agents
BlitzGraph      Supabase for graphs, built for LLM agents
Mcp360          universal MCP gateway for AI agents
AgentMail       email infra for AI agents
Voker           analytics for AI agents
x402            open standard for internet-native payments
```

Every one of these is developer tooling *for people building agents*. Not one is an agent
sold to a small business.

> **Correction, added later.** I originally wrote that Algolia was keyword-OR matching and
> simply ignoring "for SMBs". That was wrong. [ADR 005](005-query-expansion.md) has the
> measured behaviour: the index **AND**-matches terms, and only drops trailing words when
> a query returns too few results. So "AI agents for SMBs" matched almost nothing as
> written, and what came back was Algolia's fallback to roughly "AI agents" — the results
> were the degraded query, not the real one.
>
> Different mechanism, same consequence for this stage, which is exactly why the wrong
> explanation went unchallenged until an unrelated failure forced me to actually measure
> it. Worth noting as a process point: a plausible diagnosis that predicts the observed
> symptom is not a verified one.

Either way, the search was working as designed and my query was never a semantic one.

This is the brief's "each source returns 2 garbage results" anti-pattern wearing a
disguise. The results aren't junk, they're *off-topic*, and the effect on the output is
the same: a partner opens the index and finds twelve memos about the wrong market.

## Options I considered

1. **Let stage 2 sort it out.** The thesis explicitly excludes developer tooling, so
   these would all score low and land in Pass. Honest, and it needs no new code — but it
   pays full Opus-plus-web-research price to discover that an MCP gateway isn't an SMB
   bookkeeping agent, which is knowable from its one-line description. Ten wasted analyses
   per run.
2. **Better keyword queries.** Expand "AI agents for SMBs" into a set of narrower
   searches ("AI bookkeeping", "automated invoicing"). This needs a domain-term expansion
   from somewhere — a hardcoded map that only works for topics I anticipated, or a model
   call, at which point option 3 is simpler and more general.
3. **A relevance screen between the stages.** One cheap model call sees all candidates'
   names, sites and one-liners at once, and drops the obvious mismatches.

## Decision

Option 3, with the bar set deliberately low.

The screen is a single call at `effort: low`, no web search — it judges from the
one-liner, which is all that is needed to tell "MCP gateway for agent developers" from
"files restaurant tax returns". Verdicts are three-way:

- `in` — plausibly within the thesis's segment. Keep.
- `adjacent` — arguably related, unclear from the one-liner. **Keep.**
- `out` — clearly a different market or buyer. Drop.

**Only `out` is dropped**, which is the important part. This is not a scoring stage and it
must not become one: the thesis rubric in stage 2 is where candidates get judged, and a
screen that dropped anything borderline would quietly pre-empt that with a cheap call and
no evidence. The screen's only job is to stop the pipeline spending Opus tokens on a
company whose own one-line description already rules it out.

Economics: one call of roughly 2k tokens replaces something like eight full analyses,
each of which is 15–40k tokens plus web searches. It pays for itself several times over
on the first run.

## Why the drops are recorded, not deleted

Every dropped candidate goes into the run manifest with the screen's reason, and the run
index lists them under "Screened out". A sourcing layer that silently discards half its
results looks identical to one that never found them, and the difference matters a lot
when you are judging whether the pipeline works. `--no-screen` turns it off entirely.

## What this costs

- **A model can be wrong about relevance**, and a false `out` is a company that never
  gets a memo. Mitigated by keeping `adjacent`, and by the drops being visible and
  reversible — the candidate is still in `candidates.json`, so `--no-screen --force`
  re-analyses everything.
- **It is a fourth thing happening in a three-stage pipeline.** I've kept it inside stage
  1 rather than making it a stage of its own: sourcing's job is to produce *candidate
  startups*, and something in the wrong market was never a candidate. It shares stage 1's
  output file and its `--force` semantics.
- **It introduces an LLM call into a stage that was deterministic**, which means stage 1
  is no longer free to re-run. That is a real loss, and the reason it's skippable.
