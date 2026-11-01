package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/llm"
)

// newTestClient builds a client against srv with retries disabled.
func newTestClient(t *testing.T, srv *httptest.Server, opts ...func(*Options)) *Client {
	t.Helper()

	options := Options{BaseURL: srv.URL, Timeout: 5 * time.Second}
	for _, apply := range opts {
		apply(&options)
	}

	client, err := New(options)
	require.NoError(t, err)
	return client
}

// captureBody reads and returns the request body as decoded JSON.
func captureBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()

	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded
}

func simpleRequest() llm.Request {
	return llm.Request{
		Model:    "astral-projection",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "scan example.com"}},
	}
}

func Test_Complete_returns_content_and_served_model_when_the_gateway_resolves_a_combo(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"cutad/deepseek-v4.1-flash",
			"choices":[{"index":0,"message":{"role":"assistant","content":"COSMICS_OK"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "COSMICS_OK", resp.Message.Content)
	assert.Equal(t, "cutad/deepseek-v4.1-flash", resp.ModelServed,
		"the served model must come from the response, not the request")
	assert.NotEqual(t, "astral-projection", resp.ModelServed, "the combo resolved to a different model")
	assert.Equal(t, llm.FinishStop, resp.FinishReason)
	assert.Equal(t, 1, resp.Attempts)
}

func Test_Complete_maps_usage_details_when_present(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":100,"completion_tokens":20,
				"prompt_tokens_details":{"cached_tokens":64},
				"completion_tokens_details":{"reasoning_tokens":9}}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, 100, resp.Usage.InputTokens)
	assert.Equal(t, 20, resp.Usage.OutputTokens)
	assert.Equal(t, 64, resp.Usage.CachedTokens, "cached tokens must survive the mapping")
	assert.Equal(t, 9, resp.Usage.ReasoningTokens)
}

func Test_Complete_parses_tool_call_arguments_into_json_when_the_arguments_are_a_string(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[
				{"id":"call_0","type":"function","function":{"name":"run_nmap","arguments":"{\"target\":\"example.com\",\"top_ports\":100}"}},
				{"id":"call_1","type":"function","function":{"name":"run_httpx","arguments":"{\"target\":\"example.com\"}"}},
				{"id":"call_2","type":"function","function":{"name":"run_subfinder","arguments":"{\"domain\":\"example.com\"}"}},
				{"id":"call_3","type":"function","function":{"name":"run_nuclei","arguments":"{\"target\":\"example.com\",\"severity\":\"high\"}"}}
			]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":50,"completion_tokens":40}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	require.Len(t, resp.Message.ToolCalls, 4, "the gateway returns parallel tool calls")

	assert.Equal(t, "run_nmap", resp.Message.ToolCalls[0].Name)
	assert.JSONEq(t, `{"target":"example.com","top_ports":100}`, string(resp.Message.ToolCalls[0].Arguments))
	assert.Equal(t, llm.FinishTools, resp.FinishReason)
}

func Test_Complete_returns_ErrNotRetryable_when_the_provider_rejects_the_request(t *testing.T) {
	// Given
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid JSON body","type":"invalid_request_error","code":"bad_request"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(o *Options) { o.MaxRetries = 3 })

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrNotRetryable)
	assert.Equal(t, int32(1), hits.Load(), "a permanent failure must not be retried")
}

func Test_Complete_retries_when_the_provider_is_temporarily_unavailable(t *testing.T) {
	// Given
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited","code":"rate_limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(o *Options) { o.MaxRetries = 1 })
	client.sleep = func(context.Context, time.Duration) error { return nil }

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Message.Content)
	assert.Equal(t, int32(2), hits.Load())
	assert.Equal(t, 2, resp.Attempts)
}

func Test_Complete_honours_RetryAfter_when_the_provider_supplies_it(t *testing.T) {
	// Given
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"slow down","code":"rate_limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(o *Options) { o.MaxRetries = 1 })

	var waited time.Duration
	client.sleep = func(_ context.Context, d time.Duration) error {
		waited = d
		return nil
	}

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, 7*time.Second, waited, "the server-directed wait must win over exponential backoff")
}

func Test_Complete_passes_the_bearer_token_when_a_key_is_configured(t *testing.T) {
	// Given
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, func(o *Options) { o.APIKey = "sk-test-value" })

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test-value", seen)
}

func Test_Complete_omits_authorization_when_no_key_is_configured(t *testing.T) {
	// Given
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Authorization"]
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.False(t, present, "a local gateway runs without a key; an empty header is not sent")
}

func Test_Complete_sends_the_configured_model_and_puts_system_first(t *testing.T) {
	// Given
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	req := simpleRequest()
	req.System = "you are a recon agent"
	req.MaxTokens = 512

	// When
	_, err := client.Complete(context.Background(), req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "astral-projection", body["model"])
	assert.InDelta(t, 512, body["max_tokens"], 0)

	messages, ok := body["messages"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, messages)

	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "system", first["role"])
	assert.Equal(t, "you are a recon agent", first["content"])
}

func Test_New_rejects_a_relative_base_url(t *testing.T) {
	// Given
	opts := Options{BaseURL: "/v1"}

	// When
	_, err := New(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOptions)
}

func Test_New_rejects_a_negative_retry_count(t *testing.T) {
	// Given
	opts := Options{BaseURL: "http://localhost:20128/v1", MaxRetries: -1}

	// When
	_, err := New(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOptions)
}

func Test_Complete_returns_an_error_when_the_response_has_no_choices(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[],"usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrRetryable)
}

func Test_Complete_propagates_a_tool_arguments_error_when_the_string_is_not_json(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[
				{"id":"call_bad","type":"function","function":{"name":"run_nmap","arguments":"{not json"}}
			]},"finish_reason":"tool_calls"}],
			"usage":{}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.Error(t, err)

	var argsErr *ToolArgumentsError
	require.True(t, errors.As(err, &argsErr), "expected a typed tool-arguments error, got %v", err)
	assert.Equal(t, "call_bad", argsErr.CallID)
}
