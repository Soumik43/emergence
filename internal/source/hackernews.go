package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Soumik43/emergence/internal/model"
)

// HackerNews sources candidates from the HN Algolia search API.
//
// Chosen because search and traction signal arrive in the same unauthenticated call:
// every hit carries points, comment count, date, and the startup's own URL. See
// docs/decisions/001-two-sources-not-twelve.md.
type HackerNews struct {
	// BaseURL is the Algolia endpoint. Overridden in tests.
	BaseURL string
	HTTP    *http.Client
}

const (
	hnDefaultBase = "https://hn.algolia.com/api/v1"
	// hnDefaultMinPoints filters drive-by posts that nobody engaged with. Low on
	// purpose: for a niche SMB workflow, 5 points can still be a real launch, and
	// stage 2 is where weak candidates actually get rejected.
	hnDefaultMinPoints = 5
	// hnPagesPerTag bounds how deep we page per query shape. Two pages of 50 is
	// plenty to fill a 20-candidate list after filtering.
	hnPagesPerTag = 2
	hnHitsPerPage = 50
)

func NewHackerNews() *HackerNews {
	return &HackerNews{
		BaseURL: hnDefaultBase,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (h *HackerNews) Name() string { return "hackernews" }

// hnHit is the subset of the Algolia story object we use.
type hnHit struct {
	ObjectID    string   `json:"objectID"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Author      string   `json:"author"`
	Points      int      `json:"points"`
	NumComments int      `json:"num_comments"`
	CreatedAtI  int64    `json:"created_at_i"`
	Tags        []string `json:"_tags"`
}

func (h hnHit) isShowHN() bool {
	for _, t := range h.Tags {
		if t == "show_hn" {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(h.Title), "show hn")
}

func (h hnHit) permalink() string {
	return "https://news.ycombinator.com/item?id=" + h.ObjectID
}

type hnResponse struct {
	Hits []hnHit `json:"hits"`
}

// Fetch queries HN twice per page: once restricted to Show HN (launch announcements,
// the strongest signal that a company is real and shipping) and once across all stories
// (organic discussion, which catches companies other people are talking about). The two
// result sets are merged and deduplicated by website host.
func (h *HackerNews) Fetch(ctx context.Context, seed Seed) ([]model.Candidate, error) {
	base := h.BaseURL
	if base == "" {
		base = hnDefaultBase
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	minPoints := seed.MinSignal
	if minPoints <= 0 {
		minPoints = hnDefaultMinPoints
	}

	// show_hn first: a launch post is a better candidate than a discussion thread,
	// and dedupe keeps whichever hit arrives with the stronger signal anyway.
	tagSets := []string{"show_hn", "story"}

	// One topic becomes several short queries, because the index AND-matches terms and
	// a partner's phrasing is too long to match anything. See expandQuery.
	queries := expandQuery(seed.Query)

	var hits []hnHit
	for _, q := range queries {
		for _, tags := range tagSets {
			for page := 0; page < hnPagesPerTag; page++ {
				batch, err := h.query(ctx, client, base, q, tags, page)
				if err != nil {
					// A failure partway through shouldn't discard the results
					// already gathered — partial sourcing beats none.
					if len(hits) > 0 {
						break
					}
					return nil, err
				}
				hits = append(hits, batch...)
				if len(batch) < hnHitsPerPage {
					break // last page
				}
			}
		}
	}

	return h.toCandidates(hits, seed, minPoints), nil
}

func (h *HackerNews) query(ctx context.Context, client *http.Client, base, query, tags string, page int) ([]hnHit, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("tags", tags)
	q.Set("hitsPerPage", fmt.Sprint(hnHitsPerPage))
	q.Set("page", fmt.Sprint(page))
	endpoint := strings.TrimSuffix(base, "/") + "/search?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build hn request: %w", err)
	}
	req.Header.Set("User-Agent", "emergence-pipeline/0.1 (VC triage take-home)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hn search %q tags=%s: %w", query, tags, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hn search %q tags=%s: unexpected status %s", query, tags, resp.Status)
	}

	var decoded hnResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode hn response: %w", err)
	}
	return decoded.Hits, nil
}

// toCandidates applies the quality filter and collapses hits to one candidate per
// company. This function is where "10–20 real companies" is won or lost, so each
// rejection reason is separated out and tested.
func (h *HackerNews) toCandidates(hits []hnHit, seed Seed, minPoints int) []model.Candidate {
	// byHost accumulates every surviving hit for a company so multiple posts become
	// multiple signals on one candidate rather than duplicate candidates.
	byHost := map[string]*model.Candidate{}
	var order []string

	// The two query shapes overlap by construction: a Show HN post is also a story, so
	// it comes back from both tags=show_hn and tags=story. Without this, every launch
	// post is counted as two signals and the candidate looks twice as well-received as
	// it is.
	seenPosts := map[string]bool{}

	for _, hit := range hits {
		if seenPosts[hit.ObjectID] {
			continue
		}
		seenPosts[hit.ObjectID] = true

		host, ok := companyHost(hit.URL)
		if !ok {
			continue // self-post, unparseable, or a non-company domain
		}
		if hit.Points < minPoints {
			continue
		}

		signal := model.Signal{
			Kind:   model.SignalHNDiscussion,
			Detail: fmt.Sprintf("HN discussion %q — %d points, %d comments", hit.Title, hit.Points, hit.NumComments),
			Date:   time.Unix(hit.CreatedAtI, 0).UTC(),
			URL:    hit.permalink(),
		}
		if hit.isShowHN() {
			signal.Kind = model.SignalHNLaunch
			signal.Detail = fmt.Sprintf("Show HN launch — %d points, %d comments", hit.Points, hit.NumComments)
		}

		if existing, seen := byHost[host]; seen {
			existing.Signals = append(existing.Signals, signal)
			// A launch post describes the company better than a discussion thread,
			// so let it overwrite a weaker hit's name and one-liner.
			if signal.Kind == model.SignalHNLaunch && !hasLaunchSignal(existing.Signals[:len(existing.Signals)-1]) {
				name, oneLiner := parseTitle(hit.Title, host)
				existing.Name = name
				existing.OneLiner = oneLiner
			}
			continue
		}

		name, oneLiner := parseTitle(hit.Title, host)
		cand := &model.Candidate{
			ID:           slug(name),
			Name:         name,
			Website:      "https://" + host,
			OneLiner:     oneLiner,
			Founders:     []string{}, // stage 2 researches the team; HN gives us a poster, not a founder
			Signals:      []model.Signal{signal},
			Source:       model.SourceRef{Name: h.Name(), Query: seed.Query, URL: hit.permalink()},
			DiscoveredAt: time.Now().UTC(),
		}
		byHost[host] = cand
		order = append(order, host)
	}

	out := make([]model.Candidate, 0, len(order))
	for _, host := range order {
		out = append(out, *byHost[host])
	}

	// Rank by best signal so that truncating to Limit keeps the strongest candidates.
	sort.SliceStable(out, func(i, j int) bool {
		return candidateRank(out[i]) > candidateRank(out[j])
	})

	if seed.Limit > 0 && len(out) > seed.Limit {
		out = out[:seed.Limit]
	}
	return out
}

func hasLaunchSignal(signals []model.Signal) bool {
	for _, s := range signals {
		if s.Kind == model.SignalHNLaunch {
			return true
		}
	}
	return false
}

// candidateRank scores a candidate for ordering only — it is not the thesis score.
// A launch post outranks a discussion thread, and recency breaks ties, because a
// two-year-old thread is a worse lead than last month's launch.
func candidateRank(c model.Candidate) float64 {
	var rank float64
	for _, s := range c.Signals {
		weight := 1.0
		if s.Kind == model.SignalHNLaunch {
			weight = 2.0
		}
		// Half-life of one year, so freshness matters without erasing older signals.
		ageYears := time.Since(s.Date).Hours() / (24 * 365)
		rank += weight / (1 + ageYears)
	}
	return rank
}

// nonCompanyHosts are domains that show up constantly in HN results but are never the
// startup itself — blogs, repos, aggregators, papers. Without this filter, a topic
// query returns mostly essays about the topic, which is exactly the "each source
// returns 2 garbage results" failure the brief warns about.
var nonCompanyHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "news.ycombinator.com": true,
	"medium.com": true, "substack.com": true, "dev.to": true, "hashnode.dev": true,
	"youtube.com": true, "youtu.be": true, "twitter.com": true, "x.com": true,
	"reddit.com": true, "linkedin.com": true, "producthunt.com": true,
	"arxiv.org": true, "openai.com": true, "anthropic.com": true,
	"techcrunch.com": true, "theverge.com": true, "wsj.com": true, "bloomberg.com": true,
	"nytimes.com": true, "forbes.com": true, "businessinsider.com": true,
	"wikipedia.org": true, "docs.google.com": true, "notion.so": true,
	"ycombinator.com": true, "stripe.com": true, "aws.amazon.com": true,
	// App-store and package-registry listings. A company may well be real, but the
	// listing is not its site, and it dedupes separately from the real domain — a
	// live run produced both "rtrvr.ai" and its Chrome Web Store page as two
	// candidates for the same company.
	"chromewebstore.google.com": true, "chrome.google.com": true,
	"apps.apple.com": true, "play.google.com": true, "microsoft.com": true,
	"npmjs.com": true, "pypi.org": true, "marketplace.visualstudio.com": true,
	"huggingface.co": true, "replit.com": true, "vercel.app": true,
}

// companyHost extracts the normalised host of a startup's own site, reporting false
// when the URL isn't one.
func companyHost(raw string) (string, bool) {
	if raw == "" {
		return "", false // an HN self-post (Ask HN, text-only Show HN) has no company site
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if nonCompanyHosts[host] {
		return "", false
	}
	// Catch subdomains of excluded hosts (foo.github.io, blog.medium.com) by also
	// testing the registrable-looking suffix. Deliberately naive: a public-suffix
	// list is a dependency this doesn't need for a 20-candidate list.
	if parts := strings.Split(host, "."); len(parts) > 2 {
		if nonCompanyHosts[strings.Join(parts[len(parts)-2:], ".")] {
			return "", false
		}
	}
	if strings.HasSuffix(host, ".github.io") || strings.HasSuffix(host, ".substack.com") ||
		strings.HasSuffix(host, ".medium.com") || strings.HasSuffix(host, ".notion.site") {
		return "", false
	}
	return host, true
}

// titleSeparators are the dividers HN posters use between a product name and its
// pitch, longest-first so an em dash isn't matched by a bare hyphen.
var titleSeparators = []string{" — ", " – ", " -- ", " - ", ": ", " | "}

// parseTitle splits an HN title into a company name and a one-liner.
//
// "Show HN: ReplyLoop – AI support agent for Shopify stores"
//
//	→ ("ReplyLoop", "AI support agent for Shopify stores")
//
// When the title has no separator there is no name in it to find, so the host becomes
// the name and the whole title becomes the one-liner.
func parseTitle(title, host string) (name, oneLiner string) {
	t := strings.TrimSpace(title)

	// Strip the "Show HN:" / "Launch HN:" prefix.
	for _, prefix := range []string{"show hn:", "launch hn:", "show hn -", "ask hn:"} {
		if len(t) >= len(prefix) && strings.EqualFold(t[:len(prefix)], prefix) {
			t = strings.TrimSpace(t[len(prefix):])
			break
		}
	}

	// Titles carry an accelerator batch parenthetical — "Foo (YC W25) – pitch",
	// "Portal (SPC F25)". Drop it so it doesn't end up inside the company name, and
	// match any short all-caps-plus-batch shape rather than just YC.
	t = stripBatchTag(t)

	for _, sep := range titleSeparators {
		if i := strings.Index(t, sep); i > 0 {
			candidateName := strings.TrimSpace(t[:i])
			rest := strings.TrimSpace(t[i+len(sep):])
			// A long left-hand side is a sentence, not a product name.
			if candidateName != "" && rest != "" && len(strings.Fields(candidateName)) <= 4 {
				return candidateName, rest
			}
			break
		}
	}

	return hostToName(host), t
}

// batchTag matches an accelerator batch parenthetical: "(YC W25)", "(SPC F25)",
// "(a16z S24)". Kept tight — an accelerator abbreviation plus a season-and-year code —
// so it can't eat a real parenthetical like "(open source)".
var batchTag = regexp.MustCompile(`\s*\([A-Za-z0-9]{1,6}\s+[WSF]\d{2}\)`)

func stripBatchTag(title string) string {
	return strings.TrimSpace(batchTag.ReplaceAllString(title, ""))
}

// hostToName turns "replyloop.ai" into "Replyloop" — a placeholder the analysis stage
// corrects once it has read the company's own site.
func hostToName(host string) string {
	label := host
	if i := strings.Index(label, "."); i > 0 {
		label = label[:i]
	}
	label = strings.NewReplacer("-", " ", "_", " ").Replace(label)
	if label == "" {
		return host
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// slug produces a stable, filesystem-safe candidate ID.
func slug(s string) string {
	var b strings.Builder
	lastDash := true // suppresses a leading dash
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
