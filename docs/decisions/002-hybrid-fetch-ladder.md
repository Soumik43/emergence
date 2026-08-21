# 002 — Hybrid fetch: cheap local first, server-side on escalation

**Status:** accepted (revised — see *Revision* at the bottom) · **Stage:** sourcing, analysis

## Context

I started this assuming acquisition was the hard part: startup sites behind Cloudflare,
founder bios rendered client-side, LinkedIn blocking everything. The plan I walked in with
was Firecrawl or a headless browser, plus a proxy pool, plus a parser per site shape. That
is a week of work on the least interesting part of the problem.

Second problem, same shape: **the pipeline needs to search the open web, and a Go binary is
not a search engine.** That seemed to imply a SERP vendor (SerpAPI/Brave/Exa) as another
key to configure and rate-limit.

## Decision

**A three-tier fetch ladder. Always start at the cheapest tier that can answer, and
escalate only on a recorded, specific failure.**

| Tier | Mechanism | Cost | Used when |
|---|---|---|---|
| 1 | Plain Go `net/http` GET + HTML→text (`internal/fetch`) | free, ~200ms | Always tried first for any URL we already have |
| 2 | Anthropic server-side `web_fetch_20260209` | tokens | Tier 1 was blocked or returned a JS shell |
| 3 | Anthropic server-side `web_search_20260209` | tokens | We have no URL — founder background, funding, customer names |

Tier 1 handles the majority of startup landing pages, because a marketing site *wants* to
be fetched and read. It is the tier that gets us the product description, the pricing page,
and usually the about page, for nothing.

Tier 2 exists because a minority of sites will 403 a Go client or ship an empty
`<div id="root">`. Rather than fight that locally, the URL is handed to Claude's
server-side fetcher: the request then originates from Anthropic's infrastructure, which is
a normal well-behaved client. No proxy pool, no browser, no Cloudflare arms race on my side.

Tier 3 is discovery. There is no local tier for "which fund led their seed round" — that
needs a search engine, and the one attached to the model I'm already calling is strictly
better than adding a SERP vendor on a separate call with no orchestration.

## Escalation is a decision, not a retry

`internal/fetch` does not silently fall through. It classifies the tier-1 outcome and
returns a typed reason, which is recorded in the trace:

- `blocked` — 403/429/503, or a Cloudflare interstitial detected in the body
- `js_shell` — 200 OK but extracted text is below the useful-content threshold
- `transport` — DNS failure, TLS error, timeout
- `ok` — usable text, no escalation

So the memo's provenance can say *this claim came from tier 1* or *tier 1 was blocked, so
tier 2 read it*, and a reviewer can see how much of an analysis rests on which mechanism.
Escalation rate is also a cost signal: if most candidates escalate, tier 1 needs work.

## What this still costs

1. **Non-determinism at tiers 2 and 3.** The same search on two days returns different
   results. The trace files (`runs/<id>/trace/`) pin what was actually seen on the run that
   produced the committed memos, so the run is auditable even though it isn't
   bit-reproducible.
2. **Tier 1 is a text extractor, not a browser.** It gets prose, not content behind a
   click or a login. That's the boundary between tier 1 and tier 2, and it's fine.
3. **I don't control tiers 2 and 3.** If Anthropic's fetcher can't read a site either, the
   result is "no evidence" — which is why the thesis treats missing evidence as a scored
   penalty with a confidence value attached rather than as an error. The bad-data path is
   the *common* path here, not the edge case.

## Rejected

- **Firecrawl** — good product, but it prices and gates a job tier 1 does for free on most
  pages and tier 2 already covers on the rest. Third vendor, third failure mode, no new
  capability.
- **Headless Chrome (chromedp/rod)** — the right answer at 100k pages. For 20 candidates
  it is a browser binary in CI to make tier 1 marginally better at a job tier 2 already
  does. The brief's scope constraint says stop.
- **A search vendor (Exa/Tavily/Brave)** — strictly worse than the tool already available
  on the call I'm making anyway.

## Revision

The first version of this decision was *no local scraping at all* — everything through
tiers 2 and 3. Soumik pushed back: pay for the server-side path only where it earns its
keep, and use the free local one everywhere else.

He was right, and for a reason I'd missed. I'd been treating "avoid a scraping stack" and
"never fetch locally" as the same choice. They aren't: the thing worth avoiding is the
*stack* — proxies, browsers, per-site parsers, a Cloudflare arms race. A single `http.Get`
plus a text extractor is about 150 lines with no dependency beyond `x/net/html`, and it
covers most pages. Dropping it didn't buy simplicity, it just moved the same work onto a
metered API. The tier boundary is the actual design; "no scraping" was a slogan.
