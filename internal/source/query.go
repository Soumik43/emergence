package source

import "strings"

// expandQuery turns one natural-language topic into the several short queries that HN's
// search actually rewards.
//
// The Algolia index AND-matches terms, so query length works directly against recall.
// Measured against the live API:
//
//	"AI bookkeeping for restaurants"  →  2 hits
//	"AI bookkeeping"                  → 41 hits
//	"bookkeeping"                     → 50 hits
//
// A partner types the first one. Sent verbatim it yields nothing, and the run dies with
// "no candidates" on a topic that has plenty. So the topic is decomposed into the full
// term set plus its adjacent pairs, and results are unioned (deduplicated by post ID in
// toCandidates).
//
// Adjacent pairs rather than all combinations, and never single terms: "bookkeeping"
// alone returns fifty results about bookkeeping in general, which is the opposite failure
// — recall bought by throwing away the topic. Pairs keep two content words, which is
// specific enough to still be about the thing asked for.
func expandQuery(query string) []string {
	terms := contentTerms(query)

	switch len(terms) {
	case 0:
		// Nothing but stopwords. Send it through as typed and let the source
		// return whatever it returns.
		return []string{strings.TrimSpace(query)}
	case 1, 2:
		// Already short enough to match; expanding would only broaden it.
		return []string{strings.Join(terms, " ")}
	}

	// Full set first: its hits are the most on-topic, and ranking keeps them ahead
	// when the list is truncated to --limit.
	queries := []string{strings.Join(terms, " ")}

	for i := 0; i+1 < len(terms) && len(queries) < maxQueryVariants; i++ {
		queries = append(queries, terms[i]+" "+terms[i+1])
	}

	return queries
}

// maxQueryVariants bounds the API calls one topic can produce. Each variant costs two
// tag-shapes × two pages, so this keeps a run to a dozen or so cheap requests.
const maxQueryVariants = 4

// stopwords are the function words that carry no retrieval signal but do count against
// an AND match.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "for": true, "of": true, "in": true,
	"on": true, "to": true, "and": true, "or": true, "with": true, "by": true,
	"at": true, "from": true, "into": true, "that": true, "this": true,
	"is": true, "are": true, "be": true, "as": true, "it": true, "its": true,
	// Domain filler: present in most queries a partner would type, and matching on
	// it excludes companies that simply describe themselves differently.
	"startup": true, "startups": true, "company": true, "companies": true,
	"tool": true, "tools": true, "software": true, "platform": true,
	"solution": true, "solutions": true, "app": true, "apps": true,
}

// contentTerms lowercases the query, drops stopwords and punctuation, and returns the
// remaining terms in order.
func contentTerms(query string) []string {
	var out []string
	seen := map[string]bool{}

	for _, raw := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '+')
	}) {
		term := strings.Trim(raw, "-+")
		if term == "" || stopwords[term] || seen[term] {
			continue
		}
		// Single letters are noise in an AND match.
		if len(term) < 2 && term != "ai" {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}
