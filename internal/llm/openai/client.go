package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/llm"
)

// Defaults and bounds for one exchange.
const (
	DefaultTimeout      = 120 * time.Second
	maxResponseBytes    = 16 << 20
	maxSSELineBytes     = 1 << 20
	baseRetryDelay      = 250 * time.Millisecond
	maxRetryDelay       = 30 * time.Second
	chatCompletionsPath = "/chat/completions"
)

// ErrInvalidOptions is returned when Options cannot describe a usable client.
var ErrInvalidOptions = errors.New("openai: invalid client options")

// Options configures a Client.
type Options struct {
	// BaseURL is the API root, for example http://localhost:20128/v1.
	BaseURL string

	// APIKey is optional: a local gateway may run without authentication.
	APIKey string

	// Timeout bounds one HTTP exchange. Zero selects DefaultTimeout.
	Timeout time.Duration

	// MaxRetries is the number of ADDITIONAL attempts after the first.
	MaxRetries int
}

// Client talks to an OpenAI-compatible endpoint.
type Client struct {
	endpoint string
	apiKey   string
	http     *http.Client
	retries  int
	sleep    func(context.Context, time.Duration) error
}

// New validates the options and returns a client.
//
// A relative BaseURL is rejected rather than silently resolved against the
// process working directory, because a misconfigured gateway would otherwise
// fail with an opaque transport error at call time.
func New(opts Options) (*Client, error) {
	parsed, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("base url %q: %w", opts.BaseURL, err)
	}
	if !parsed.IsAbs() {
		return nil, fmt.Errorf("base url %q must be absolute: %w", opts.BaseURL, ErrInvalidOptions)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("base url %q must use http or https: %w", opts.BaseURL, ErrInvalidOptions)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("base url %q has no host: %w", opts.BaseURL, ErrInvalidOptions)
	}
	if opts.MaxRetries < 0 {
		return nil, fmt.Errorf("max retries %d must not be negative: %w", opts.MaxRetries, ErrInvalidOptions)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	return &Client{
		endpoint: strings.TrimRight(opts.BaseURL, "/") + chatCompletionsPath,
		apiKey:   opts.APIKey,
		http:     &http.Client{Timeout: timeout},
		retries:  opts.MaxRetries,
		sleep:    sleepContext,
	}, nil
}

// Kind reports the wire protocol.
func (c *Client) Kind() llm.ProviderKind { return llm.KindOpenAICompatible }

// Complete performs one non-streaming request.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("complete: %w", err)
	}

	body, err := json.Marshal(toWireRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	started := time.Now()

	resp, attempts, err := c.roundTrip(ctx, body, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := readLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var decoded wireResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if decoded.Error != nil {
		return nil, newStatusError(resp.StatusCode, *decoded.Error, 0)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("response has no choices: %w", llm.ErrRetryable)
	}

	message, err := fromWireMessage(decoded.Choices[0].Message)
	if err != nil {
		return nil, err
	}

	return &llm.Response{
		Message:      message,
		FinishReason: normalizeFinish(decoded.Choices[0].FinishReason),
		Usage:        fromWireUsage(decoded.Usage),
		ModelServed:  decoded.Model,
		Latency:      time.Since(started),
		Attempts:     attempts,
	}, nil
}

// Stream performs one streaming request, forwarding chunks to sink.
//
// Retries happen only before a response body is opened. Once any chunk has been
// delivered the call cannot be replayed without duplicating output, so a failure
// mid-stream is returned rather than retried.
func (c *Client) Stream(ctx context.Context, req llm.Request, sink llm.ChunkSink) (*llm.Response, error) {
	if sink == nil {
		return nil, fmt.Errorf("stream: nil sink: %w", llm.ErrNotRetryable)
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}

	body, err := json.Marshal(toWireRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	started := time.Now()

	resp, attempts, err := c.roundTrip(ctx, body, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxResponseBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)

	parser := newStreamParser()

	for scanner.Scan() {
		chunks, err := parser.feed(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("stream: %w", err)
		}
		if err := forward(sink, chunks); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	chunks, err := parser.finalize()
	if err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}
	if err := forward(sink, chunks); err != nil {
		return nil, err
	}

	message, err := parser.message()
	if err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}

	return &llm.Response{
		Message:      message,
		FinishReason: parser.finishReason(),
		Usage:        parser.usage(),
		ModelServed:  parser.model(),
		Latency:      time.Since(started),
		Attempts:     attempts,
	}, nil
}

// forward delivers chunks to the sink, attributing a rejection to the chunk kind.
func forward(sink llm.ChunkSink, chunks []llm.Chunk) error {
	for _, chunk := range chunks {
		if err := sink(chunk); err != nil {
			return fmt.Errorf("sink rejected %s chunk: %w", chunk.Kind, err)
		}
	}
	return nil
}
