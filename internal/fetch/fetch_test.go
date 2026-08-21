package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve spins up a test server with one handler and returns a fetcher pointed at it.
func serve(t *testing.T, h http.HandlerFunc) (*Fetcher, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(), srv.URL
}

// A landing page with real prose is the case tier 1 exists for: no escalation, text
// extracted, title captured.
func TestGetExtractsLandingPage(t *testing.T) {
	body := `<!doctype html><html><head><title>ReplyLoop — support autopilot</title>
	<script>var tracking = "<div>not text</div>";</script>
	<style>.a{color:red}</style></head>
	<body><nav><a>Pricing</a><a>Docs</a></nav>
	<h1>Support autopilot for Shopify stores</h1>
	<p>ReplyLoop answers your customer emails end to end. It reads the order, decides the
	refund, issues it, and replies — no draft for you to approve.</p>
	<p>Founded by a former Gorgias support lead who answered 40,000 tickets by hand.</p>
	<p>Trusted by 120 stores processing over two million dollars a month in orders.</p>
	</body></html>`

	f, url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})

	got := f.Get(context.Background(), url)

	if got.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %q (%s), want ok", got.Outcome, got.Detail)
	}
	if got.Outcome.ShouldEscalate() {
		t.Error("ShouldEscalate() = true for a readable page")
	}
	if got.Title != "ReplyLoop — support autopilot" {
		t.Errorf("Title = %q", got.Title)
	}
	if !strings.Contains(got.Text, "issues it, and replies") {
		t.Errorf("body prose missing from Text:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "Gorgias support lead") {
		t.Error("founder sentence missing — this is the evidence stage 2 grades on")
	}
	// Script and style contents are markup, not prose. Leaking them wastes prompt
	// budget and invents claims.
	if strings.Contains(got.Text, "tracking") || strings.Contains(got.Text, "color:red") {
		t.Errorf("script/style content leaked into Text:\n%s", got.Text)
	}
	// Adjacent nav items must not fuse into one word.
	if strings.Contains(got.Text, "PricingDocs") {
		t.Error("block boundaries lost: nav items concatenated")
	}
}

func TestGetClassifiesEscalationReasons(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    Outcome
	}{
		{
			name: "403 is blocked",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			want: OutcomeBlocked,
		},
		{
			// The band the threshold has to separate: a genuinely sparse but real
			// landing page must not be mistaken for a shell.
			name: "sparse but real landing page is ok",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html><head><title>Ledgerly</title></head><body>
				<h1>Close your books without a bookkeeper.</h1>
				<ul><li>Categorises every transaction automatically.</li>
				<li>Files your quarterly return for you.</li>
				<li>Flags anything it is not sure about before filing.</li></ul>
				<p>Built for restaurants doing under five million a year.</p>
				</body></html>`))
			},
			want: OutcomeOK,
		},
		{
			name: "429 is blocked",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			want: OutcomeBlocked,
		},
		{
			name: "cf-mitigated header is blocked even on a 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("cf-mitigated", "challenge")
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body>" + strings.Repeat("filler text ", 100) + "</body></html>"))
			},
			want: OutcomeBlocked,
		},
		{
			name: "cloudflare interstitial body is blocked despite 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html><head><title>Just a moment...</title></head>
				<body><p>Enable JavaScript and cookies to continue</p>` +
					strings.Repeat("<p>padding so it clears the length floor</p>", 20) +
					`</body></html>`))
			},
			want: OutcomeBlocked,
		},
		{
			name: "empty react root is a js shell",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html><head><title>App</title></head>
				<body><div id="root"></div><script src="/bundle.js"></script></body></html>`))
			},
			want: OutcomeJSShell,
		},
		{
			name: "pdf is not html",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/pdf")
				_, _ = w.Write([]byte("%PDF-1.7"))
			},
			want: OutcomeNotHTML,
		},
		{
			name: "404 is transport, not blocked",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			want: OutcomeTransport,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, url := serve(t, tc.handler)

			got := f.Get(context.Background(), url)

			if got.Outcome != tc.want {
				t.Errorf("Outcome = %q (detail %q), want %q", got.Outcome, got.Detail, tc.want)
			}
			if got.Outcome.ShouldEscalate() != (tc.want != OutcomeOK) {
				t.Errorf("ShouldEscalate() = %v for outcome %q", got.Outcome.ShouldEscalate(), got.Outcome)
			}
			// Every escalation must carry a reason, or the trace can't explain why a
			// tier-2 call was made.
			if tc.want != OutcomeOK && got.Detail == "" {
				t.Error("Detail is empty; the trace needs a reason for the escalation")
			}
		})
	}
}

// A dead host must come back as a Result, not an error — callers handle escalation in
// one place, and "the site is gone" is itself a finding about the company.
func TestGetOnUnreachableHostReturnsResult(t *testing.T) {
	f := New()

	got := f.Get(context.Background(), "https://this-domain-should-not-resolve.invalid")

	if got.Outcome != OutcomeTransport {
		t.Errorf("Outcome = %q, want transport", got.Outcome)
	}
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0 for a request that never completed", got.Status)
	}
	if got.Detail == "" {
		t.Error("Detail is empty")
	}
}

// A domain that now redirects elsewhere is a real signal (acquired, parked, rebranded),
// so the post-redirect URL has to survive into the result.
func TestGetRecordsFinalURLAfterRedirect(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><p>" +
			strings.Repeat("We have been acquired and this product is now part of BigCo. ", 12) +
			"</p></body></html>"))
	}))
	t.Cleanup(dest.Close)

	f, url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	})

	got := f.Get(context.Background(), url)

	if got.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %q (%s), want ok", got.Outcome, got.Detail)
	}
	if got.FinalURL != dest.URL+"/" && got.FinalURL != dest.URL {
		t.Errorf("FinalURL = %q, want the redirect target %q", got.FinalURL, dest.URL)
	}
}

func TestGetTruncatesLongPages(t *testing.T) {
	f, url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><p>" +
			strings.Repeat("verbose marketing copy that goes on and on. ", 2000) +
			"</p></body></html>"))
	})
	f.MaxTextBytes = 500

	got := f.Get(context.Background(), url)

	if got.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %q, want ok", got.Outcome)
	}
	if !strings.HasSuffix(got.Text, "[truncated]") {
		t.Error("long page was not marked as truncated")
	}
	if len(got.Text) > 600 {
		t.Errorf("Text is %d bytes, want ~500 plus the marker", len(got.Text))
	}
}

func TestExtract(t *testing.T) {
	t.Run("malformed html still yields text", func(t *testing.T) {
		// Unclosed tags are the norm in the wild; the extractor must degrade rather
		// than return nothing.
		_, text := extract(`<html><body><p>first para<p>second para<div>third`)

		for _, want := range []string{"first para", "second para", "third"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in %q", want, text)
			}
		}
	})

	t.Run("nested dropped elements do not unbalance the skip counter", func(t *testing.T) {
		// A <script> whose string contents look like tags used to end the skip early
		// and dump JavaScript into the prose.
		_, text := extract(`<html><body>
			<script>if (a) { document.write("<script>nested</script>"); }</script>
			<p>real content here</p></body></html>`)

		if !strings.Contains(text, "real content here") {
			t.Errorf("content after a tricky script was dropped: %q", text)
		}
		if strings.Contains(text, "document.write") {
			t.Errorf("script body leaked: %q", text)
		}
	})

	t.Run("whitespace is collapsed", func(t *testing.T) {
		_, text := extract("<html><body><p>a  \t  b</p>\n\n\n<p>c</p></body></html>")

		if strings.Contains(text, "  ") {
			t.Errorf("double space survived: %q", text)
		}
		if strings.Contains(text, "\n\n\n") {
			t.Errorf("blank-line run survived: %q", text)
		}
		if !strings.Contains(text, "a b") {
			t.Errorf("want %q to contain \"a b\"", text)
		}
	})

	t.Run("empty document", func(t *testing.T) {
		title, text := extract("")
		if title != "" || text != "" {
			t.Errorf("extract(\"\") = (%q, %q), want empty", title, text)
		}
	})
}
