package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/llm"
)

// newTestClient builds a client against srv with retries disabled.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()

	options := Options{BaseURL: srv.URL, Timeout: 5 * time.Second}

	client, err := New(options)
	require.NoError(t, err)
	return client
}

// simpleRequest is one user turn.
func simpleRequest() llm.Request {
	return llm.Request{
		Model:    "cutad/deepseek-v4.1-flash",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "scan example.com"}},
	}
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

func Test_Complete_merges_thinking_text_and_tool_blocks_when_all_three_are_present(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"id":"msg_1","type":"message","role":"assistant",
			"model":"cutad/deepseek-v4.1-flash",
			"content":[
				{"type":"thinking","thinking":"The target needs a port scan.","signature":"sig"},
				{"type":"text","text":"Running a port scan."},
				{"type":"tool_use","id":"toolu_1","name":"run_nmap","input":{"target":"example.com"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":120,"output_tokens":48}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)

	assert.Equal(t, "The target needs a port scan.", resp.Message.Reasoning,
		"the thinking block must be captured")
	assert.Equal(t, "Running a port scan.", resp.Message.Content,
		"the text block must be captured too, not dropped after the first block")
	require.Len(t, resp.Message.ToolCalls, 1, "the tool_use block must not be lost")
	assert.Equal(t, "run_nmap", resp.Message.ToolCalls[0].Name)
	assert.Equal(t, "toolu_1", resp.Message.ToolCalls[0].ID)
	assert.JSONEq(t, `{"target":"example.com"}`, string(resp.Message.ToolCalls[0].Arguments))
	assert.Equal(t, llm.FinishTools, resp.FinishReason)
}

func Test_Complete_defaults_max_tokens_when_the_request_omits_it(t *testing.T) {
	// Given
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.InDelta(t, float64(DefaultMaxTokens), body["max_tokens"], 0,
		"an omitted max_tokens would otherwise be silently capped at 2048")
}

func Test_Complete_sends_the_system_prompt_at_the_top_level_when_system_is_set(t *testing.T) {
	// Given
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	req := simpleRequest()
	req.System = "you are a recon agent"

	// When
	_, err := client.Complete(context.Background(), req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "you are a recon agent", body["system"])

	messages, ok := body["messages"].([]any)
	require.True(t, ok)
	for _, raw := range messages {
		message, isMap := raw.(map[string]any)
		require.True(t, isMap)
		assert.NotEqual(t, "system", message["role"],
			"the Messages API rejects a system message inside the array")
	}
}

func Test_Complete_maps_stop_reason_tool_use_to_FinishTools(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m",
			"content":[{"type":"tool_use","id":"toolu_9","name":"run_httpx","input":{}}],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":10,"output_tokens":5}
		}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, llm.FinishTools, resp.FinishReason)
	require.Len(t, resp.Message.ToolCalls, 1)
	assert.JSONEq(t, `{}`, string(resp.Message.ToolCalls[0].Arguments))
}

func Test_Complete_maps_end_turn_to_FinishStop(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, llm.FinishStop, resp.FinishReason)
}

func Test_Complete_translates_a_tool_result_into_a_user_message_with_tool_result_blocks(t *testing.T) {
	// Given
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	req := llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "scan"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: "toolu_1", Name: "run_nmap", Arguments: json.RawMessage(`{"target":"example.com"}`),
			}}},
			{Role: llm.RoleTool, ToolID: "toolu_1", Content: "22/tcp open ssh"},
		},
	}

	// When
	_, err := client.Complete(context.Background(), req)

	// Then
	require.NoError(t, err)

	messages, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 3)

	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", first["role"])

	second, ok := messages[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "assistant", second["role"])

	third, ok := messages[2].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", third["role"], "a tool result travels as a user turn")

	blocks, ok := third["content"].([]any)
	require.True(t, ok)
	require.Len(t, blocks, 1)

	block, ok := blocks[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tool_result", block["type"])
	assert.Equal(t, "toolu_1", block["tool_use_id"])
}

func Test_Complete_passes_the_message_count_without_split_system_when_only_messages_exist(t *testing.T) {
	// Given
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	_, present := body["system"]
	assert.False(t, present, "an absent system prompt must not be sent as an empty string")
}

func Test_Complete_returns_an_error_when_the_request_lacks_a_body(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: at least one message is required"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_newClient_rejects_a_missing_anthropic_version(t *testing.T) {
	// Given
	opts := Options{BaseURL: "https://api.anthropic.com/v1"}

	// When
	_, err := newClient(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOptions)
}

func Test_New_applies_the_documented_version_when_omitted(t *testing.T) {
	// Given
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("anthropic-version")
		_, _ = io.WriteString(w, `{"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, DefaultAnthropicVersion, seen)
}

func Test_Complete_records_cached_input_tokens_when_they_are_reported(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":64}
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
	assert.Equal(t, 64, resp.Usage.CachedTokens)
}

func Test_Complete_reports_the_served_model_when_the_provider_substitutes_one(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"served-by-gateway","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	resp, err := client.Complete(context.Background(), simpleRequest())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "served-by-gateway", resp.ModelServed)
	assert.NotEqual(t, "cutad/deepseek-v4.1-flash", resp.ModelServed)
}
