# Process

**How this file was made:** I built the pipeline in one session with Claude Code, then went
back through that session with Claude to write this. The quotes are my real messages, copied
verbatim. Claude drafted the connective prose from that record; I chose the framing and cut it
down. I would rather say that than have you wonder.
[AI_ATTRIBUTION.md](AI_ATTRIBUTION.md) discloses the same for the code, which Claude wrote
almost all of.

The honest summary is that we were both wrong at different points and mostly caught each
other.

## Stack, and what I got wrong first

Go because it is what I write fastest and least badly in. On a timeboxed take-home that
mattered more than any property of the language. Cobra because a subcommand per stage is the
obvious shape and I did not want to hand-roll flag parsing.

My first instinct about the work was wrong twice over:

> "id assume we have to primarily web-scrape such details right, we have might have to
> bypass cloudflare or mulitple things at this point right? we can use firecrawl or
> something else, and how do we properly search as we are not an llm in itself"

I had decided acquisition was the hard part, so I was braced for proxies and a headless
browser. And I thought search was a blocker, since a Go binary is not a search engine, so I
assumed a paid SERP API. Neither held. Hacker News has a free Algolia endpoint where the
results carry the traction signal in the same response, and a plain `http.Get` plus a text
extractor reads most landing pages, because a marketing site wants to be read.

Reaching for the hard version first is still the right reflex, applied too early. It cost
nothing because I asked instead of building. Had I spent a day on a scraping stack before
checking whether I needed one, the same reflex would have been the most expensive mistake
here.

## Where I overrode the model

Claude's first design removed local fetching entirely. Every page read would go through the
model's server-side `web_fetch`, and it wrote a confident ADR for this: no scraping stack, no
proxy pool, no Cloudflare arms race. It read well. I said no:

> "its not necessary that we want to go through a no scraping stack, we would rather go
> with a hybrid model for the both, and when needed we wisely use what we want to"

Three reasons, and the first is the one I would defend. "Never fetch locally" is a rule, and
this does not call for a rule. Some pages are trivially readable and some are not, you can
look at which is which, and a blanket policy throws away information you already have. Then
cost, because paying a metered API to read a public marketing page is wasteful at twenty
companies and worse at two hundred. Then control, because when you outsource the fetch you
cannot see or fix what happens when it fails.

Pushed on it, Claude found the error in its own reasoning quickly, and it is recorded in
[ADR 002](decisions/002-hybrid-fetch-ladder.md): it had treated "avoid a scraping stack" and
"never fetch locally" as one choice. What is worth avoiding is the stack, the proxies and
browsers and per-site parsers. A local fetch plus an extractor is about a hundred and fifty
lines. Dropping it did not buy simplicity, it moved the same work onto a metered API.

What shipped is a three tier ladder: local first, escalate on a classified failure, and record
which tier answered.

## What that taught me about reviewing agent output

The ADR for the design I rejected was well argued. Structured, specific, honest about
tradeoffs, and wrong. It was not sloppy work I caught by noticing sloppiness. The reasoning
was fluent and the conclusion was bad, so fluency is not a signal I can use.

Which raises where else that is sitting uncaught, and I think I know.
[AI_ATTRIBUTION.md](AI_ATTRIBUTION.md#what-claude-decided-without-review) lists what went in
with no second pair of eyes. The item that worries me is the thesis weights. Founder proximity
at 25, traction at 10, four others. Those six numbers decide every score the pipeline
produces. They are argued for in [THESIS.md](THESIS.md), and the argument is plausible in
exactly the way the fetch ADR was plausible. I did not review them, and I could not defend
them in a partner meeting on anything better than "they sound about right", which is not a
defence.

## The bug no test would have caught

Stage 1 was built, unit tested, green. Then we ran it against `--query "AI agents for SMBs"`
and got twelve real companies, all of them developer tooling for people building agents. MCP
gateways, agent observability, email infrastructure for agents. Not one sold to a small
business.

The code was working exactly as written. What was broken sat upstream of anything a unit test
sees. That produced the relevance screen ([ADR 004](decisions/004-relevance-screen.md)) and
then, after a second run showed the opposite failure, query decomposition
([ADR 005](decisions/005-query-expansion.md)). I should have run stage 1 the moment it
compiled, before anything was built on top of it.

## The thing that happened twice

ADR 004 originally gave the wrong mechanism. It said Algolia was OR matching and diluting "for
SMBs", which explained the symptom and was wrong. It AND matches, and only drops trailing
words when a query underfills. Three curl calls settled that, and nobody made them until an
unrelated failure forced it.

Then the same shape elsewhere. Pushing this repo kept failing with `Repository not found`.
First diagnosis, the repo did not exist: true, fixed, still failed. Second, the token could
not see it: also true, also fixed, still failed. The real cause was Xcode shipping a system
gitconfig with `credential.helper = osxkeychain`, git accumulating credential helpers rather
than replacing them, and the system one answering first with my work account.

Two independent instances, so a pattern rather than bad luck. A diagnosis that explains the
symptom is not a verified diagnosis. Both times the wrong one survived because it predicted
what we were seeing, and both times the check that would have killed it took under a minute.

## What is not finished

**The rubric has never been calibrated**, not against one company whose outcome is known.
There is no evidence a 72 means anything different from a 65. The arithmetic is auditable and
the weights are not validated, which are different properties.

**One source behind an interface built for several.** `source.Source` has one implementation.
That is speculative generality until the second exists, and I left it because the YC batch
feed is obviously next, which is what everyone says right before they never add it.

**The relevance screen is a patch.** It works and it is cheap, and it exists because of a bad
live result rather than because anyone thought about relevance up front.

## If I did it again

Run every stage against real data the moment it compiles. Both genuinely bad problems here
were found by running the thing, neither by a test.

And spend the review on the judgement calls rather than the code. I put my attention on the
code, which the tests already covered, and almost none on the six numbers that decide every
output. Code either works or it does not and something tells you. A weight of 25 that should
be 15 will never announce itself.
