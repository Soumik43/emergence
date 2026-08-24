# AI attribution

The brief says: *"If an agent wrote a module end-to-end, say so. We don't penalize that;
we penalize hiding it."* So, precisely.

This document is a factual record of who wrote what. The reflective part is
[PROCESS.md](PROCESS.md), and that file opens by disclosing its own authorship, which is
mixed: Claude drafted the prose from the session transcript, Soumik chose the framing,
decided what was worth admitting, and cut it. Its direct quotes are his real messages from
the session, verbatim.

## The short version

**Claude (Fable 5, via Claude Code) wrote essentially all of the Go in this repository,
end to end, including the tests.** Soumik directed the work, made the stack and scoping
calls, and overrode the architecture once in a way that changed the design (see below).
Nothing here was typed line-by-line by a human and then attributed to a model, and
nothing was written by a model and presented as hand-written.

## What Claude produced

| Area | Authorship |
|---|---|
| All Go source under `internal/`, `cmd/` | Claude, end to end |
| All tests | Claude, end to end |
| Prompt templates in `prompts/` | Claude |
| `docs/THESIS.md` — the thesis and rubric | Claude drafted; Soumik set the constraint that it be narrow enough to be falsifiable |
| `docs/decisions/*.md` | Claude, describing decisions Claude mostly made |
| `README.md`, `Makefile` | Claude |
| Commit messages | Claude |
| `docs/PROCESS.md` | **Mixed, and it says so in its own first paragraph.** Claude drafted the prose from the session transcript; Soumik set the framing, chose the admissions, and cut it. Quotes in it are his real session messages |
| This file | Claude |

## What Soumik decided

These were his calls, not the model's:

1. **Go, with Cobra for the CLI.** Claude did not propose the stack.
2. **A commit per working stage**, so the history is revertable stage by stage rather than
   one drop.
3. **The hybrid fetch ladder.** This is the substantive one. Claude's first design was
   *no local fetching at all* — every page read through the model's server-side
   `web_fetch`, on the reasoning that this avoids a scraping stack entirely. Soumik
   rejected it: use the free local path where it works, pay for the server-side one only
   where it's needed, and decide per case.

   He was right, and the reason is recorded in
   [ADR 002's revision note](decisions/002-hybrid-fetch-ladder.md#revision): Claude had
   conflated "avoid a scraping stack" with "never fetch locally". Those are different
   choices. The thing worth avoiding is the *stack* — proxies, headless browsers, per-site
   parsers. A single `http.Get` plus a text extractor is ~150 lines and covers most
   landing pages; dropping it didn't buy simplicity, it moved the same work onto a metered
   API. The tier boundary became the actual design.

4. **Raising the framing question up front** — "we'll have to bypass Cloudflare, and how
   do we search when we aren't a search engine?" Both turned out to have cheaper answers
   than the ones he was braced for, but the pipeline's whole acquisition design came out of
   taking those two questions seriously first.

## What Claude decided without review

Named for honesty, since a reviewer should know which choices had one pair of eyes on
them and which had two:

- Hacker News via the Algolia API as the single source, and the non-company domain filter
- Scoring in Go from model-graded criteria, rather than asking for a number ([ADR 003](decisions/003-score-in-code.md))
- The six criteria and their weights (the weights in particular are an assertion, argued
  in THESIS.md but not validated against anything)
- Missing evidence scoring 1/5 rather than the midpoint, and the confidence mechanism
- The relevance screen ([ADR 004](decisions/004-relevance-screen.md)), added in response
  to a bad live result rather than planned
- Wire-level tracing via HTTP middleware
- Every threshold: `MinUsefulText = 120`, `hnDefaultMinPoints = 5`, band boundaries at
  70/45, `--concurrency 3`
- What to test and what not to

## How it was worked, mechanically

Claude Code, one long session, with:

- **`claude-api` skill loaded before writing any SDK code.** Claude's training-time
  knowledge of the Anthropic Go SDK was not trusted for exact type names — the skill
  supplied `OutputConfigParam`, `WebSearchTool20260209Param`, adaptive thinking, and the
  strict-tool shape. The SDK surface was then grepped directly out of the module cache to
  confirm field names before writing, rather than guessing and compiling repeatedly.
- **Tests written alongside each stage, not after**, and run before each commit.
- **A live smoke run of stage 1** after building it, which is what exposed the relevance
  problem that ADR 004 exists to fix.

## Bugs the tests caught, and who found them

Worth listing because it's the most honest signal about where the agent was wrong. Every
one of these was written by Claude and caught by a test Claude wrote:

| Bug | Why it mattered |
|---|---|
| `head` was in the dropped-elements set, so `<title>` — nested inside it — was never captured | Every page title came back empty |
| Inline elements emitted no text boundary | Adjacent nav links extracted as `PricingDocs` |
| The js-shell threshold was set from a guess (400 chars) | The sparse-page fixture measured 230, so real pages would have been misclassified and re-fetched at cost |
| `tags=show_hn` and `tags=story` overlap by construction | Every launch post counted twice; candidates looked twice as well-received as they were |
| `sortedFailures` returned a map that the caller then iterated | Defeated its own purpose — the "sorted" output was still randomly ordered, and would have produced noise diffs in committed files |

And one caught by running it rather than by a test: stage 1's first live run returned
twelve developer-tooling companies for an SMB query. No unit test would have found that,
because the code was working exactly as written.
