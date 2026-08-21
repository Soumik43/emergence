// Package llm wraps the Anthropic API for this pipeline's one use: ask the model to
// research a company and return a typed answer.
//
// It owns three things the analysis stage shouldn't have to think about — the tool loop,
// the tiers 2 and 3 server tools (web_fetch and web_search, see
// docs/decisions/002-hybrid-fetch-ladder.md), and the trace file that makes every claim
// checkable.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel is Claude Opus 5. Chosen over a cheaper tier because the judgement in
// stage 2 — grading founder proximity from a landing page and a LinkedIn bio — is the
// part of the pipeline where model quality actually shows up in the output.
const DefaultModel = "claude-opus-5"

// ErrNoCredentials is returned when no API key is configured, with the fix in the
// message rather than a bare 401 from the transport.
var ErrNoCredentials = errors.New("no ANTHROPIC_API_KEY set: export one, or run `ant auth login`")

// Client is a thin wrapper over the Anthropic SDK.
type Client struct {
	Model string
	// Effort maps to output_config.effort. High is the default: this is a judgement
	// task with web research, which is exactly where effort pays for itself.
	Effort anthropic.OutputConfigEffort
	// MaxTokens caps a single response.
	MaxTokens int64
	// MaxTurns bounds the tool loop, so a model that keeps searching without ever
	// submitting can't run forever on someone's API bill.
	MaxTurns int

	apiKey string
}

func New() *Client {
	return &Client{
		Model:     DefaultModel,
		Effort:    anthropic.OutputConfigEffortHigh,
		MaxTokens: 16000,
		MaxTurns:  12,
		apiKey:    os.Getenv("ANTHROPIC_API_KEY"),
	}
}

// Request is one research-and-answer call.
type Request struct {
	// Label names the call in the trace file, e.g. "analyze/ledgerly".
	Label string
	// System is the instruction set. Cached, since it is identical across every
	// candidate in a run — 20 candidates means 19 cache hits on it.
	System string
	// User is the per-candidate prompt: what we already know, and what to find out.
	User string
	// AnswerTool is the tool the model must call to deliver its answer. Its input is
	// returned as Result.Answer. Declared strict, so the input validates against the
	// schema and doesn't need defensive parsing.
	AnswerTool anthropic.ToolParam
	// WebSearch and WebFetch enable tiers 3 and 2 respectively.
	WebSearch bool
	WebFetch  bool
}

// Result is what a completed call yields.
type Result struct {
	// Answer is the raw JSON input of the AnswerTool call. Empty if the model never
	// called it — check Refused and Text to find out why.
	Answer json.RawMessage
	// Text is any prose the model emitted alongside, useful when Answer is empty.
	Text string
	// Refused is set when a safety classifier declined the request.
	Refused       bool
	RefusalReason string
	// TracePath is where the exchange log was written, relative paths resolved by the
	// caller. Recorded in the memo so a reader can find the provenance.
	TracePath string

	Turns        int
	InputTokens  int
	OutputTokens int
	WebSearches  int
	WebFetches   int
	DurationMS   int64
}

// Do runs the tool loop until the model calls the answer tool, and writes the trace to
// tracePath regardless of outcome.
//
// Server-side tools need no round trip from us: web_search and web_fetch execute on
// Anthropic's infrastructure and their results arrive as content blocks in the same
// response. The only tool we handle locally is the answer tool, which ends the loop. So
// this loop is short by design — it exists to absorb pause_turn and to stop a model that
// won't commit.
func (c *Client) Do(ctx context.Context, req Request, tracePath string) (Result, error) {
	res := Result{TracePath: tracePath}
	if c.apiKey == "" {
		return res, ErrNoCredentials
	}

	rec := NewRecorder(req.Label, c.Model)
	defer func() {
		if err := rec.Write(tracePath); err != nil {
			// A trace we couldn't write is a provenance gap, not a failed analysis.
			fmt.Fprintf(os.Stderr, "warning: could not write trace %s: %v\n", tracePath, err)
		}
	}()

	client := anthropic.NewClient(
		option.WithAPIKey(c.apiKey),
		rec.Middleware(),
		// Web research turns are slow; the SDK default is 10 minutes, which a
		// multi-search turn can brush against.
		option.WithRequestTimeout(15*time.Minute),
	)

	tools := []anthropic.ToolUnionParam{{OfTool: &req.AnswerTool}}
	if req.WebSearch {
		tools = append(tools, anthropic.ToolUnionParam{
			OfWebSearchTool20260209: &anthropic.WebSearchTool20260209Param{},
		})
	}
	if req.WebFetch {
		tools = append(tools, anthropic.ToolUnionParam{
			OfWebFetchTool20260209: &anthropic.WebFetchTool20260209Param{},
		})
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: c.MaxTokens,
		System: []anthropic.TextBlockParam{{
			Text: req.System,
			// The system prompt is byte-identical for every candidate in a run, and
			// it carries the whole rubric, so it is the one thing worth caching.
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.User)),
		},
		Tools:        tools,
		Thinking:     anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		OutputConfig: anthropic.OutputConfigParam{Effort: c.Effort},
	}

	start := time.Now()
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	for turn := 1; turn <= c.MaxTurns; turn++ {
		res.Turns = turn

		msg, err := client.Messages.New(ctx, params)
		if err != nil {
			rec.Fail(err)
			return res, fmt.Errorf("%s: turn %d: %w", req.Label, turn, err)
		}

		res.InputTokens += int(msg.Usage.InputTokens)
		res.OutputTokens += int(msg.Usage.OutputTokens)
		res.WebSearches += int(msg.Usage.ServerToolUse.WebSearchRequests)
		res.WebFetches += int(msg.Usage.ServerToolUse.WebFetchRequests)

		// A refusal is an HTTP 200 with a stop reason, not an error, so it has to be
		// checked before reading content.
		if msg.StopReason == anthropic.StopReasonRefusal {
			res.Refused = true
			res.RefusalReason = string(msg.StopDetails.Category)
			if msg.StopDetails.Explanation != "" {
				res.RefusalReason += ": " + msg.StopDetails.Explanation
			}
			rec.Fail(fmt.Errorf("refused: %s", res.RefusalReason))
			return res, nil
		}

		for _, block := range msg.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.TextBlock:
				res.Text += variant.Text
			case anthropic.ToolUseBlock:
				if variant.Name == req.AnswerTool.Name {
					res.Answer = json.RawMessage(variant.JSON.Input.Raw())
					return res, nil
				}
			}
		}

		params.Messages = append(params.Messages, msg.ToParam())

		switch msg.StopReason {
		case anthropic.StopReasonPauseTurn:
			// The server paused a long-running turn mid-flight. Send the history
			// straight back to resume; nothing for us to add.
			continue
		case anthropic.StopReasonToolUse:
			// A tool_use we didn't return from means the model called something
			// other than the answer tool. Server tools resolve server-side, so
			// there is nothing to feed back — but returning here without a nudge
			// would loop on an identical request. Ask it to commit.
			params.Messages = append(params.Messages, anthropic.NewUserMessage(
				anthropic.NewTextBlock("Submit your answer now by calling "+
					req.AnswerTool.Name+", using the evidence you have. Grade any "+
					"criterion you could not find evidence for as no_evidence rather "+
					"than searching further."),
			))
		default:
			// end_turn or max_tokens with no answer tool call: the model wrote prose
			// instead of committing. One nudge, then the loop bound catches it.
			params.Messages = append(params.Messages, anthropic.NewUserMessage(
				anthropic.NewTextBlock("You did not call "+req.AnswerTool.Name+
					". Call it now with your findings."),
			))
		}
	}

	err := fmt.Errorf("%s: model never called %s within %d turns", req.Label, req.AnswerTool.Name, c.MaxTurns)
	rec.Fail(err)
	return res, err
}

// HasCredentials reports whether a key is configured, so the CLI can fail fast with a
// useful message before doing any sourcing work.
func (c *Client) HasCredentials() bool { return c.apiKey != "" }
