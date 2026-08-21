# 005 — Decompose the topic; a partner's phrasing is not a search query

**Status:** accepted · **Stage:** sourcing

## Context

With the relevance screen ([ADR 004](004-relevance-screen.md)) in place, I ran a second,
narrower topic — the kind a partner would actually type when they have a specific thesis
in mind:

```
$ emergence source --query "AI bookkeeping for restaurants"
error: sourcing found no candidates — try a broader topic or a lower --min-signal
```

Zero candidates, on a topic where good companies plainly exist. So stage 1 now had two
opposite failure modes: broad queries returned the wrong market, and specific queries
returned nothing at all.

## What is actually happening

I measured it against the live API instead of reasoning about it, which I should have done
the first time:

| Query | Hits |
|---|---:|
| `AI bookkeeping for restaurants` | **2** |
| `AI bookkeeping` | 41 |
| `bookkeeping` | 50 |

The index **AND**-matches terms. Every extra word is another conjunct a story title has to
satisfy, so query length works directly against recall — and a natural-language topic is
mostly words no title contains. Algolia additionally drops trailing words when a query
returns too few results, which is what produced ADR 004's original symptom: "AI agents for
SMBs" matched almost nothing as typed, so the results I saw were the fallback to roughly
"AI agents".

That correction matters more than the fix. My first diagnosis — OR matching, "for SMBs"
diluted — predicted the symptom correctly and was still wrong about the mechanism. It
survived a whole ADR because it was never tested against three curl calls.

## Decision

Decompose the topic into the full term set plus its **adjacent pairs**, and union the
results (deduplicated by post ID, which `toCandidates` already did).

```
"AI bookkeeping for restaurants"
  → "ai bookkeeping restaurants"   (full set — most on-topic, ranked first)
  → "ai bookkeeping"
  → "bookkeeping restaurants"
```

Stopwords go first, including domain filler — `software`, `platform`, `tool`, `startup`.
Those match against companies that merely describe themselves in different words, so they
cost recall and buy no precision.

**Never single terms, and capped at four variants.** `bookkeeping` alone returns fifty
results about bookkeeping in general, which is recall bought by discarding the topic — the
same failure as ADR 004 in the other direction. Pairs keep two content words, which stays
about the thing that was asked for. The cap bounds cost: each variant is two tag-shapes ×
two pages.

## Result

Same query, after:

```
Indiebooks      indiebooks.io    Free bookkeeping that auto-fills CRA/IRS tax forms
Midday          midday.ai        Receipt-to-transaction matching, open source
ParsePoint      parsepoint.app   AI OCR that pipes any invoice straight into Excel
LedgerIQ        ledgeriq.ai      How AI is changing bookkeeping
Duetbrowser     duetbrowser.com  HTML, CSS and JavaScript in the terminal
Elder Dragon's  …stavern.com     My D&D character tracker
```

Four real candidates in the target market, from a query that previously returned nothing.

The last two are the price of the broader net — and they are precisely what the relevance
screen is for. The two mechanisms compose the way they should: expansion buys recall, the
screen pays for it in precision, and both leave a record of what they did.

## What this costs

- **Up to four times the API calls** per run. They're free and unauthenticated, so this is
  latency, not money — a few extra seconds in the cheapest stage.
- **Adjacent pairs are a heuristic about word order**, and it assumes the topic reads as a
  modifier chain ("AI bookkeeping restaurants"). A topic phrased as a sentence would pair
  words that don't belong together. Acceptable for a CLI where the input is a topic, and
  visible when it misfires because the screen's drop reasons show what came back.
- **It makes stage 1 less predictable** to a user: what you type is not what gets searched.
  Mitigated by the run manifest recording the query as typed, and by every candidate
  carrying the `source.query` that surfaced it.

## Rejected

- **LLM query expansion** — ask a model for domain synonyms ("bookkeeping" → "accounting",
  "reconciliation", "close the books"). Genuinely better recall, and the obvious next step
  if this were real. Not now: it puts a second model call in front of the screen's model
  call, and the deterministic version already turned a zero-result query into four real
  candidates. Do the free thing first and see whether the paid thing is still needed.
- **Lowering `--min-signal`** — the error message's own suggestion, and it treats a recall
  problem as a threshold problem. It would have added low-scoring posts from the same two
  hits rather than finding the other thirty-nine.
