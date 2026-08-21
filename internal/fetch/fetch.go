// Package fetch is tier 1 of the fetch ladder: a plain HTTP GET plus HTML-to-text
// extraction, with no browser, proxy, or per-site parser.
//
// Most startup landing pages are marketing sites that want to be read, so this tier
// answers most requests for free. When it can't, it does not silently retry — it returns
// a typed Outcome saying why, and the caller escalates to the model's server-side fetcher
// (tier 2). See docs/decisions/002-hybrid-fetch-ladder.md.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Outcome classifies a tier-1 attempt. It is recorded in the run trace so a reviewer can
// see which mechanism produced the evidence behind any claim.
type Outcome string

const (
	// OutcomeOK means usable text was extracted; no escalation needed.
	OutcomeOK Outcome = "ok"
	// OutcomeBlocked means the server refused us: 403/429/503, or a bot interstitial
	// in the body. Escalate — Anthropic's fetcher is a different, well-behaved client.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeJSShell means 200 OK but the page is a client-rendered skeleton with
	// nothing to read. Escalate.
	OutcomeJSShell Outcome = "js_shell"
	// OutcomeTransport means DNS, TLS, or timeout failure. Escalate — though if the
	// host is genuinely down, tier 2 will fail too, and "no evidence" is the answer.
	OutcomeTransport Outcome = "transport"
	// OutcomeNotHTML means the URL served something we don't extract (PDF, image).
	// Escalate: the model's fetcher handles PDFs.
	OutcomeNotHTML Outcome = "not_html"
)

// ShouldEscalate reports whether this outcome warrants a tier-2 attempt.
func (o Outcome) ShouldEscalate() bool { return o != OutcomeOK }

// Result is one tier-1 fetch attempt.
type Result struct {
	URL     string  `json:"url"`
	Outcome Outcome `json:"outcome"`
	// Status is the HTTP status code, or 0 if the request never completed.
	Status int `json:"status"`
	// Title is the document title, useful even when Text is thin.
	Title string `json:"title,omitempty"`
	// Text is extracted, whitespace-collapsed page text, truncated to MaxTextBytes.
	Text string `json:"text,omitempty"`
	// Detail explains a non-OK outcome in one line, for the trace.
	Detail string `json:"detail,omitempty"`
	// FinalURL is the post-redirect URL, which is how you notice a startup's domain
	// now points at an acquirer.
	FinalURL   string `json:"final_url,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Fetcher performs tier-1 fetches.
type Fetcher struct {
	HTTP *http.Client
	// MinUsefulText is the extracted-character floor below which a 200 response is
	// treated as a JS shell. A real landing page clears this comfortably; a
	// `<div id="root">` skeleton does not.
	MinUsefulText int
	// MaxTextBytes truncates extracted text so one verbose page can't dominate the
	// analysis prompt.
	MaxTextBytes int
	// MaxBodyBytes caps how much of a response we read, so a stray large file can't
	// exhaust memory.
	MaxBodyBytes int64
}

const (
	// defaultMinUsefulText separates a client-rendered shell from a real page.
	//
	// Measured against the fixtures in fetch_test.go rather than guessed: an empty
	// `<div id="root">` plus a bundle script extracts to well under 50 characters,
	// while a deliberately sparse real landing page (hero line, three bullets, one
	// qualifier) comes in around 230. I first set this to 400 on the assumption that
	// sparse pages ran ~300, and the sparse fixture failed — so the floor now sits in
	// the measured gap, nearer the shell end.
	//
	// Biasing low is also the cheaper error. A false "js_shell" burns a tier-2 call on
	// a page already in hand; a false "ok" just means the model reads a thin page and
	// reports thin evidence, which the confidence score already accounts for.
	defaultMinUsefulText = 120
	defaultMaxTextBytes  = 12000
	defaultMaxBodyBytes  = 4 << 20 // 4 MiB
	// A real browser UA. Not evasion: a default Go user-agent is refused by a lot of
	// CDNs outright, and the point of tier 1 is to read public marketing pages the
	// same way a visitor would.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/122.0 Safari/537.36 emergence-pipeline/0.1"
)

func New() *Fetcher {
	return &Fetcher{
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
			// Cap redirects; a redirect loop is a blocked outcome, not a hang.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("stopped after 5 redirects")
				}
				return nil
			},
		},
		MinUsefulText: defaultMinUsefulText,
		MaxTextBytes:  defaultMaxTextBytes,
		MaxBodyBytes:  defaultMaxBodyBytes,
	}
}

// Get performs one tier-1 fetch. It returns a Result for every input, including
// failures: an error here would force callers to handle "blocked" twice, and blocked is
// an expected outcome rather than an exception.
func (f *Fetcher) Get(ctx context.Context, rawURL string) Result {
	start := time.Now()
	res := Result{URL: rawURL, Outcome: OutcomeTransport}
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	client := f.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		res.Detail = fmt.Sprintf("bad url: %v", err)
		return res
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		res.Detail = firstLine(err.Error())
		return res
	}
	defer resp.Body.Close()

	res.Status = resp.StatusCode
	if resp.Request != nil && resp.Request.URL != nil {
		res.FinalURL = resp.Request.URL.String()
	}

	// Cloudflare marks its own challenges, which saves guessing from the body.
	if resp.Header.Get("cf-mitigated") == "challenge" {
		res.Outcome = OutcomeBlocked
		res.Detail = "cloudflare challenge (cf-mitigated header)"
		return res
	}

	switch {
	case resp.StatusCode == http.StatusForbidden,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusUnauthorized:
		res.Outcome = OutcomeBlocked
		res.Detail = fmt.Sprintf("http %d", resp.StatusCode)
		return res
	case resp.StatusCode >= 400:
		// 404/410/5xx are not a bot wall — the page is simply not there. Tier 2
		// won't find it either, but escalating is cheap and occasionally the model
		// locates the moved page, so this stays escalatable.
		res.Outcome = OutcomeTransport
		res.Detail = fmt.Sprintf("http %d", resp.StatusCode)
		return res
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" && !isHTML(ct) {
		res.Outcome = OutcomeNotHTML
		res.Detail = "content-type " + ct
		return res
	}

	maxBody := f.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		res.Outcome = OutcomeTransport
		res.Detail = "read body: " + firstLine(err.Error())
		return res
	}

	title, text := extract(string(body))
	res.Title = title

	if interstitial := detectInterstitial(title, text); interstitial != "" {
		res.Outcome = OutcomeBlocked
		res.Detail = interstitial
		return res
	}

	minText := f.MinUsefulText
	if minText <= 0 {
		minText = defaultMinUsefulText
	}
	if len(text) < minText {
		res.Outcome = OutcomeJSShell
		res.Detail = fmt.Sprintf("only %d chars of text extracted (min %d)", len(text), minText)
		res.Text = text // keep the scraps; a title plus 80 chars is still a weak signal
		return res
	}

	maxText := f.MaxTextBytes
	if maxText <= 0 {
		maxText = defaultMaxTextBytes
	}
	if len(text) > maxText {
		text = text[:maxText] + " …[truncated]"
	}

	res.Outcome = OutcomeOK
	res.Text = text
	return res
}

func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") ||
		strings.Contains(ct, "application/xhtml") ||
		strings.Contains(ct, "text/plain")
}

// interstitialMarkers are phrases that only appear on bot walls and challenge pages.
// Kept specific: a false positive here wastes a tier-2 call on a page we already had.
var interstitialMarkers = []string{
	"just a moment",
	"attention required! | cloudflare",
	"checking your browser before accessing",
	"enable javascript and cookies to continue",
	"verifying you are human",
	"ddos protection by cloudflare",
	"please turn javascript on and reload the page",
	"access denied",
	"request blocked",
	"are you a robot",
	"perimeterx",
	"captcha-delivery",
}

func detectInterstitial(title, text string) string {
	haystack := strings.ToLower(title + " \n " + text)
	// Only inspect the head of the body: the phrase "access denied" deep inside a
	// long article is prose, not a wall.
	if len(haystack) > 2000 {
		haystack = haystack[:2000]
	}
	for _, marker := range interstitialMarkers {
		if strings.Contains(haystack, marker) {
			return "interstitial: " + marker
		}
	}
	return ""
}

// extract pulls the title and visible text out of an HTML document.
//
// Deliberately simple: drop the elements that are never prose, keep everything else,
// collapse whitespace. No readability heuristics, no boilerplate stripping — the
// consumer is a language model that copes fine with a nav menu in the text, and every
// cleverness here would be a per-site rule in disguise.
func extract(doc string) (title, text string) {
	tokenizer := html.NewTokenizer(strings.NewReader(doc))

	// skipDepth tracks nesting inside an element whose text we discard, so a <script>
	// containing "<div>" doesn't unbalance the counter.
	var (
		b         strings.Builder
		skip      string
		skipDepth int
		inTitle   bool
	)

	// Elements whose contents are markup or chrome, never readable content.
	// Note "head" is absent deliberately: <title> lives inside it, and the other head
	// children (meta, link) emit no text tokens anyway.
	dropped := map[string]bool{
		"script": true, "style": true, "noscript": true, "svg": true,
		"iframe": true, "canvas": true, "template": true,
	}

	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			// Includes io.EOF and malformed markup; either way we're done and keep
			// whatever we gathered. Half a page beats an error.
			return strings.TrimSpace(title), collapse(b.String())

		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			tag := string(name)
			if skip != "" {
				if tag == skip {
					skipDepth++
				}
				continue
			}
			switch {
			case dropped[tag]:
				skip, skipDepth = tag, 1
			case tag == "title":
				inTitle = true
			case isBlockTag(tag):
				b.WriteByte('\n')
			default:
				// Inline elements still need a boundary, or adjacent nav links come
				// out as "PricingDocs". A space is enough; collapse() tidies runs.
				b.WriteByte(' ')
			}

		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			tag := string(name)
			if skip != "" {
				if tag == skip {
					skipDepth--
					if skipDepth <= 0 {
						skip = ""
					}
				}
				continue
			}
			if tag == "title" {
				inTitle = false
			}
			if isBlockTag(tag) {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}

		case html.TextToken:
			if skip != "" {
				continue
			}
			chunk := string(tokenizer.Text())
			if inTitle {
				title += chunk
				continue
			}
			b.WriteString(chunk)
		}
	}
}

// isBlockTag reports whether a tag implies a line break, so that "PricingContact"
// doesn't come out of adjacent nav items.
func isBlockTag(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "tr", "section", "article", "header", "footer",
		"nav", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "table", "main",
		"aside", "form", "blockquote", "figure", "hr", "td", "th", "label", "option":
		return true
	}
	return false
}

// collapse normalises whitespace: runs of spaces become one space, runs of blank lines
// become one blank line. Keeps the text compact for a prompt without losing structure.
func collapse(s string) string {
	var (
		out       strings.Builder
		pendingNL int
		lastSpace bool
	)
	for _, r := range s {
		switch r {
		case '\n', '\r':
			pendingNL++
			lastSpace = false
		case ' ', '\t', '\v', '\f', 0xa0:
			if pendingNL == 0 {
				lastSpace = true
			}
		default:
			if pendingNL > 0 && out.Len() > 0 {
				if pendingNL > 1 {
					out.WriteString("\n\n")
				} else {
					out.WriteByte('\n')
				}
			} else if lastSpace && out.Len() > 0 {
				out.WriteByte(' ')
			}
			pendingNL, lastSpace = 0, false
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
