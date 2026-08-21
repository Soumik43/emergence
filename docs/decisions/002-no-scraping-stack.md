# 002 — No scraping stack, no Cloudflare fight

**Status:** accepted · **Stage:** sourcing, analysis

## Context

I started this assuming the hard part was acquisition: startup sites are behind Cloudflare,
LinkedIn blocks everything, founder bios are rendered client-side. The plan I walked in with
was Firecrawl (or a headless browser) plus a proxy pool, plus retry/backoff, plus a parser
per site shape. That is a week of work and it is the *least* interesting week.

Second problem, same shape: **the pipeline needs to search the open web, and a Go binary is
not a search engine.** Which implied a SERP API key (SerpAPI/Brave/Exa) as another paid
dependency to configure and rate-limit.

## Decision

**Both problems are solved by moving them server-side to the model provider.** The analysis
call declares Anthropic's two server-side tools:

- `web_search_20260209` — Claude runs the search on Anthropic's infrastructure and gets
  results back with source URLs. No SERP key, no quota to manage.
- `web_fetch_20260209` — Claude fetches a URL server-side and reads the content.

So the local pipeline never makes an outbound scrape request at all. There is no Cloudflare
problem because there is no browser of mine to block; the fetch originates from Anthropic,
which is a normal well-behaved client honouring robots.txt. Zero scraping code, zero proxy
infrastructure, zero HTML parsers, one fewer vendor.

## What this costs

Honest list of what I gave up:

1. **It isn't free.** Every candidate costs real tokens, and web search is billed per use.
   Mitigated by caching every stage to disk (see [003](003-score-in-code.md) and the
   `--force` flag): a re-run of the analysis stage costs nothing for candidates already
   analysed.
2. **I don't control the fetch.** If Anthropic's fetcher can't read a site, I get "no
   evidence" and cannot drop to a headless browser to force it. This is exactly why the
   thesis treats missing evidence as a scored penalty with a confidence value attached
   rather than as a crash — the "bad or missing data" path is the *common* path here, not
   the edge case.
3. **Non-determinism.** The same query on two days returns different search results. The
   trace files (`runs/<id>/trace/`) pin down what was actually seen on the run that
   produced the committed memos, so a reviewer can audit the run even though they can't
   bit-reproduce it.

## Rejected

- **Firecrawl** — good product, but it makes the interesting problem (judgement) wait behind
  the boring one (acquisition), and adds a key + quota + failure mode I'd have to handle.
- **Headless Chrome / rod / chromedp** — the correct answer if I needed 100k pages. For 20
  candidates it is pure overengineering, and the brief's scope constraint says stop.
- **A search-API vendor (Exa/Tavily/Brave)** — strictly worse than the tool I'm already
  paying for on the same call, with no orchestration.
