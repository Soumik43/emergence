package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Soumik43/emergence/internal/model"
)

func hit(objectID, title, url string, points int, tags ...string) hnHit {
	if len(tags) == 0 {
		tags = []string{"story"}
	}
	return hnHit{
		ObjectID:    objectID,
		Title:       title,
		URL:         url,
		Points:      points,
		NumComments: points / 2,
		CreatedAtI:  time.Now().Add(-30 * 24 * time.Hour).Unix(),
		Tags:        tags,
	}
}

// hnServer serves the same hit list for every query, which is enough to exercise
// filtering, dedupe and ranking without pretending to reimplement Algolia.
func hnServer(t *testing.T, hits []hnHit) *HackerNews {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "" {
			t.Errorf("request made with no query param: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hnResponse{Hits: hits})
	}))
	t.Cleanup(srv.Close)
	return &HackerNews{BaseURL: srv.URL, HTTP: srv.Client()}
}

func fetchAll(t *testing.T, h *HackerNews, seed Seed) []model.Candidate {
	t.Helper()
	if seed.Query == "" {
		seed.Query = "AI agents for SMBs"
	}
	got, err := h.Fetch(context.Background(), seed)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return got
}

func names(cands []model.Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Name
	}
	return out
}

// The core quality filter. Without it a topic query returns mostly essays *about* the
// topic, which is the "each source returns 2 garbage results" failure from the brief.
func TestFetchRejectsNonCompanyResults(t *testing.T) {
	h := hnServer(t, []hnHit{
		hit("1", "Show HN: Ledgerly – bookkeeping autopilot for restaurants", "https://ledgerly.com", 140, "story", "show_hn"),
		hit("2", "The state of AI agents in 2026", "https://someone.substack.com/p/agents", 300),
		hit("3", "Ask HN: how do you automate invoicing?", "", 90),
		hit("4", "agent-framework: a library for tool loops", "https://github.com/foo/agent-framework", 800),
		hit("5", "Attention Is All You Need", "https://arxiv.org/abs/1706.03762", 500),
		hit("6", "OpenAI announces agent SDK", "https://openai.com/blog/agents", 900),
		hit("7", "My blog post on SMB software", "https://blog.medium.com/x", 200),
		hit("8", "Show HN: Chasely – chases your unpaid invoices", "https://chasely.io", 60, "story", "show_hn"),
		hit("9", "TechCrunch: startup raises 5M", "https://techcrunch.com/2026/01/01/x", 150),
		hit("10", "Cool project", "https://foo.github.io/project", 120),
	})

	got := fetchAll(t, h, Seed{})

	want := map[string]bool{"Ledgerly": true, "Chasely": true}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want exactly %v", len(got), names(got), want)
	}
	for _, c := range got {
		if !want[c.Name] {
			t.Errorf("non-company result survived the filter: %q (%s)", c.Name, c.Website)
		}
	}
}

// A startup that posts three times is one lead with three signals, not three leads.
func TestFetchDedupesByHostAndAccumulatesSignals(t *testing.T) {
	h := hnServer(t, []hnHit{
		hit("1", "Ledgerly is doing something interesting", "https://ledgerly.com/blog/why", 40),
		hit("2", "Show HN: Ledgerly – bookkeeping autopilot for restaurants", "https://ledgerly.com", 140, "story", "show_hn"),
		hit("3", "Ledgerly now files your quarterly return", "https://www.ledgerly.com/", 75),
	})

	got := fetchAll(t, h, Seed{})

	if len(got) != 1 {
		t.Fatalf("got %d candidates %v, want 1 (all three posts are the same company)", len(got), names(got))
	}
	c := got[0]
	if len(c.Signals) != 3 {
		t.Errorf("Signals = %d, want 3 — every post is evidence", len(c.Signals))
	}
	// The launch post is the best description of the company, so it must win the name
	// and one-liner even though a discussion thread was seen first.
	if c.Name != "Ledgerly" {
		t.Errorf("Name = %q, want %q from the Show HN title", c.Name, "Ledgerly")
	}
	if c.OneLiner != "bookkeeping autopilot for restaurants" {
		t.Errorf("OneLiner = %q, want the launch pitch", c.OneLiner)
	}
	// www. and the trailing slash are the same host.
	if c.Website != "https://ledgerly.com" {
		t.Errorf("Website = %q", c.Website)
	}

	var launches int
	for _, s := range c.Signals {
		if s.Kind == model.SignalHNLaunch {
			launches++
		}
		if s.URL == "" {
			t.Error("signal has no URL; a reviewer must be able to check it")
		}
	}
	if launches != 1 {
		t.Errorf("launch signals = %d, want 1", launches)
	}
}

func TestFetchFiltersLowSignalAndRespectsLimit(t *testing.T) {
	h := hnServer(t, []hnHit{
		hit("1", "Show HN: Alpha – a thing", "https://alpha.com", 100, "story", "show_hn"),
		hit("2", "Show HN: Beta – a thing", "https://beta.com", 50, "story", "show_hn"),
		hit("3", "Show HN: Gamma – a thing", "https://gamma.com", 2, "story", "show_hn"),
	})

	t.Run("default threshold drops the 2-point post", func(t *testing.T) {
		got := fetchAll(t, h, Seed{})
		if len(got) != 2 {
			t.Fatalf("got %v, want Alpha and Beta only", names(got))
		}
	})

	t.Run("explicit MinSignal overrides the default", func(t *testing.T) {
		got := fetchAll(t, h, Seed{MinSignal: 75})
		if len(got) != 1 || got[0].Name != "Alpha" {
			t.Fatalf("got %v, want [Alpha]", names(got))
		}
	})

	t.Run("Limit truncates after ranking, keeping the strongest", func(t *testing.T) {
		got := fetchAll(t, h, Seed{Limit: 1})
		if len(got) != 1 {
			t.Fatalf("got %d candidates, want 1", len(got))
		}
	})
}

// Ranking decides who survives truncation to Limit, so a fresh launch must outrank a
// stale discussion thread.
func TestFetchRanksLaunchesAndRecencyFirst(t *testing.T) {
	stale := hit("1", "Old news about Stale Co", "https://stale.com", 400)
	stale.CreatedAtI = time.Now().Add(-6 * 365 * 24 * time.Hour).Unix()

	fresh := hit("2", "Show HN: Fresh – invoice chasing for trades", "https://fresh.io", 30, "story", "show_hn")
	fresh.CreatedAtI = time.Now().Add(-7 * 24 * time.Hour).Unix()

	h := hnServer(t, []hnHit{stale, fresh})

	got := fetchAll(t, h, Seed{})

	if len(got) != 2 {
		t.Fatalf("got %v, want 2", names(got))
	}
	if got[0].Name != "Fresh" {
		t.Errorf("ranked %v first; a 7-day-old launch should outrank a 6-year-old thread", got[0].Name)
	}
}

func TestFetchSurfacesTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	h := &HackerNews{BaseURL: srv.URL, HTTP: srv.Client()}

	if _, err := h.Fetch(context.Background(), Seed{Query: "x"}); err == nil {
		t.Fatal("Fetch returned nil error on a 500; a broken source must not look like an empty one")
	}
}

func TestFetchOnEmptyResultsIsNotAnError(t *testing.T) {
	h := hnServer(t, nil)

	got, err := h.Fetch(context.Background(), Seed{Query: "no such topic exists"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candidates, want 0", len(got))
	}
}

func TestParseTitle(t *testing.T) {
	tests := []struct {
		name         string
		title        string
		host         string
		wantName     string
		wantOneLiner string
	}{
		{
			name:         "show hn with en dash",
			title:        "Show HN: ReplyLoop – AI support agent for Shopify stores",
			host:         "replyloop.ai",
			wantName:     "ReplyLoop",
			wantOneLiner: "AI support agent for Shopify stores",
		},
		{
			name:         "show hn with hyphen",
			title:        "Show HN: Chasely - chases your unpaid invoices",
			host:         "chasely.io",
			wantName:     "Chasely",
			wantOneLiner: "chases your unpaid invoices",
		},
		{
			name:         "launch hn strips the batch parenthetical",
			title:        "Launch HN: Ledgerly (YC W25) – bookkeeping autopilot",
			host:         "ledgerly.com",
			wantName:     "Ledgerly",
			wantOneLiner: "bookkeeping autopilot",
		},
		{
			name:         "colon separator",
			title:        "Show HN: Fixa: dispatch software for plumbers",
			host:         "fixa.app",
			wantName:     "Fixa",
			wantOneLiner: "dispatch software for plumbers",
		},
		{
			// No separator, so there is no name in the title to find; the host is
			// the honest fallback and stage 2 corrects it from the company's own site.
			name:         "no separator falls back to the host",
			title:        "Show HN: A C-Suite AI Agent Meant for SMB",
			host:         "askcaa.com",
			wantName:     "Askcaa",
			wantOneLiner: "A C-Suite AI Agent Meant for SMB",
		},
		{
			// A long left-hand side is a sentence clause, not a product name.
			name:         "sentence before a dash is not treated as a name",
			title:        "We spent two years building an agent - and here is what broke",
			host:         "someco.com",
			wantName:     "Someco",
			wantOneLiner: "We spent two years building an agent - and here is what broke",
		},
		{
			name:         "hyphenated host becomes a spaced name",
			title:        "Some untitled thing",
			host:         "close-books.io",
			wantName:     "Close books",
			wantOneLiner: "Some untitled thing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotOneLiner := parseTitle(tc.title, tc.host)
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if gotOneLiner != tc.wantOneLiner {
				t.Errorf("oneLiner = %q, want %q", gotOneLiner, tc.wantOneLiner)
			}
		})
	}
}

func TestCompanyHost(t *testing.T) {
	tests := []struct {
		raw      string
		wantHost string
		wantOK   bool
	}{
		{"https://ledgerly.com/pricing", "ledgerly.com", true},
		{"https://www.Ledgerly.com", "ledgerly.com", true},
		{"http://ledgerly.com:8080/x", "ledgerly.com", true},
		{"https://app.ledgerly.com", "app.ledgerly.com", true},
		{"", "", false},                                 // HN self-post
		{"not a url", "", false},                        // unparseable
		{"ftp://files.example.com", "", false},          // not web
		{"https://github.com/foo/bar", "", false},       // repo, not company
		{"https://foo.github.io/proj", "", false},       // project page
		{"https://someone.substack.com/p/x", "", false}, // newsletter
		{"https://news.ycombinator.com/item?id=1", "", false},
		{"https://techcrunch.com/2026/01/01/x", "", false}, // press
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			gotHost, gotOK := companyHost(tc.raw)
			if gotOK != tc.wantOK || gotHost != tc.wantHost {
				t.Errorf("companyHost(%q) = (%q, %v), want (%q, %v)",
					tc.raw, gotHost, gotOK, tc.wantHost, tc.wantOK)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ReplyLoop", "replyloop"},
		{"Close Books", "close-books"},
		{"Foo & Bar, Inc.", "foo-bar-inc"},
		{"  spaced  out  ", "spaced-out"},
		{"---", ""},
	}
	for _, tc := range tests {
		if got := slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
