package source

import (
	"reflect"
	"strings"
	"testing"
)

func TestExpandQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			// The case that broke a live run: sent verbatim this returns 2 hits and
			// the run dies with "no candidates" on a topic that has plenty.
			name:  "long query decomposes into adjacent pairs",
			query: "AI bookkeeping for restaurants",
			want:  []string{"ai bookkeeping restaurants", "ai bookkeeping", "bookkeeping restaurants"},
		},
		{
			name:  "stopwords are dropped before pairing",
			query: "AI agents for SMBs",
			want:  []string{"ai agents smbs", "ai agents", "agents smbs"},
		},
		{
			// Domain filler matches against companies that describe themselves in
			// any other words, so it costs recall for no precision.
			name:  "domain filler is treated as a stopword",
			query: "software tools for dental practices",
			want:  []string{"dental practices"},
		},
		{
			name:  "a short query is left alone",
			query: "AI bookkeeping",
			want:  []string{"ai bookkeeping"},
		},
		{
			name:  "a single term is left alone",
			query: "bookkeeping",
			want:  []string{"bookkeeping"},
		},
		{
			name:  "punctuation does not create terms",
			query: "AI agents, for SMBs!",
			want:  []string{"ai agents smbs", "ai agents", "agents smbs"},
		},
		{
			name:  "duplicate terms collapse",
			query: "AI agents for AI agents",
			want:  []string{"ai agents"},
		},
		{
			// Only "best" survives the stopword pass, so this degrades to a
			// one-term search. Garbage in, garbage out — but it must not crash or
			// return nothing, and the relevance screen will drop what it finds.
			name:  "a query that is almost all stopwords degrades to its one term",
			query: "the best software",
			want:  []string{"best"},
		},
		{
			name:  "an entirely stopword query passes through as typed",
			query: "the of and for",
			want:  []string{"the of and for"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandQuery(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("expandQuery(%q) =\n  %q\nwant\n  %q", tc.query, got, tc.want)
			}
		})
	}
}

// Recall must not be bought by discarding the topic: "bookkeeping" alone returns fifty
// results about bookkeeping in general.
func TestExpandQueryNeverEmitsASingleTermFromALongQuery(t *testing.T) {
	for _, q := range []string{
		"AI bookkeeping for restaurants",
		"agents that file taxes for small dental practices",
		"automated invoice chasing for plumbing contractors",
	} {
		for _, variant := range expandQuery(q) {
			if len(strings.Fields(variant)) < 2 {
				t.Errorf("expandQuery(%q) produced the single-term variant %q", q, variant)
			}
		}
	}
}

func TestExpandQueryIsBounded(t *testing.T) {
	got := expandQuery("automated invoice chasing for small plumbing and roofing contractors in texas")

	if len(got) > maxQueryVariants {
		t.Errorf("expandQuery produced %d variants, want at most %d — each one costs four API calls",
			len(got), maxQueryVariants)
	}
	// The full term set must survive as the first variant: its hits are the most
	// on-topic, and ranking keeps them ahead when the list is truncated to --limit.
	if len(strings.Fields(got[0])) < 3 {
		t.Errorf("first variant %q is not the full term set", got[0])
	}
}

func TestContentTerms(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"AI agents for SMBs", []string{"ai", "agents", "smbs"}},
		{"", nil},
		{"the of and", nil},
		{"go-to-market", []string{"go-to-market"}},
		{"AI  ", []string{"ai"}},
		// A bare single letter is noise in an AND match; "ai" is the exception
		// because it is a real term of art at two characters.
		{"x y AI", []string{"ai"}},
	}
	for _, tc := range tests {
		if got := contentTerms(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("contentTerms(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
