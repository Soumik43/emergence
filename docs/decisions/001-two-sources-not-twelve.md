# 001 — Two sources, not twelve

**Status:** accepted · **Stage:** sourcing

## Context

The brief lists five places partners look (Product Hunt, YC, HN, X, Crunchbase) and then
explicitly warns against "a 12-source sourcing layer where each source returns 2 garbage
results." My first instinct was still to build a plugin interface and wire up four of them,
because breadth *looks* like effort.

## Decision

**One primary source: Hacker News, via the Algolia search API.** Optionally a second
(YC directory) behind the same interface, added only if HN alone can't fill 10–20 candidates
for a topic.

## Why HN specifically

It is the only free source where **search and traction signal are the same API call**:

```
https://hn.algolia.com/api/v1/search?tags=story&query=<topic>
```

Every hit already carries `points`, `num_comments`, `created_at`, and the startup's own
`url`. So the freshness/traction requirement is satisfied by the sourcing call itself
rather than by a second enrichment pass per candidate. No API key, no rate-limit budget,
no auth, no ToS grey area.

Compare the alternatives I rejected:

| Source | Why not (now) |
|---|---|
| Crunchbase | Traction data is behind a paid tier. Free tier is name + logo. |
| Twitter/X | API is paid and hostile; scraping it is an anti-pattern under "public sources only, free tiers fine." |
| Product Hunt | Good launch signal, but its GraphQL API needs OAuth setup, and its content overlaps HN's Show HN heavily. Second-best, so it's the candidate for source #2 if HN under-delivers. |
| YC directory | Genuinely good for the "YC W25 batch" seed input, which is a *different input shape* (feed, not query). Implemented second, behind the same `Source` interface, because it exercises the batch-feed path the brief mentions. |

## The tradeoff I'm accepting

HN's population is biased: technical founders, developer-adjacent products,
English-language, launch-day-shaped. For an SMB back-office thesis
([THESIS.md](../THESIS.md)) that bias is *partly* adverse — plenty of good SMB tooling
never posts to HN. So this pipeline systematically cannot see a chunk of its own target
market.

I'd rather have that stated in one line here than have it hidden behind four half-working
scrapers. If this went to production the fix is Product Hunt as source #2, not more sources.
