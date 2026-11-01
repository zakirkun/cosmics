package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/llm"
)

// capturedSSE reproduces the frame shapes the gateway emits, including the
// keep-alive comment, a separate reasoning delta, and a trailing usage frame.
const capturedSSE = `: keep-alive

data: {"id":"chatcmpl-1","model":"cutad/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"cutad/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"reasoning_content":"The target needs a port scan."},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"cutad/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"content":"Starting "},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"cutad/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"content":"recon."},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"cutad/deepseek-v4.1-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":9,"total_tokens":51}}

data: [DONE]`

// feedAll drives a parser over a whole transcript.
func feedAll(t *testing.T, parser *streamParser, transcript string) []llm.Chunk {
	t.Helper()

	var all []llm.Chunk
	for _, line := range splitLines(transcript) {
		chunks, err := parser.feed(line)
		require.NoError(t, err)
		all = append(all, chunks...)
	}
	return all
}

// splitLines splits on newlines without dropping the empty separator lines.
func splitLines(s string) []string {
	var out []string
	current := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, current)
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

func Test_streamParser_emits_chunks_in_order_when_the_transcript_is_complete(t *testing.T) {
	// Given
	parser := newStreamParser()

	// When
	chunks := feedAll(t, parser, capturedSSE)

	// Then
	var kinds []llm.ChunkKind
	for _, chunk := range chunks {
		kinds = append(kinds, chunk.Kind)
	}

	assert.Equal(t, []llm.ChunkKind{
		llm.ChunkReasoning,
		llm.ChunkText,
		llm.ChunkText,
		llm.ChunkUsage,
		llm.ChunkDone,
	}, kinds)
}

func Test_streamParser_ignores_keep_alive_comments(t *testing.T) {
	// Given
	parser := newStreamParser()

	// When
	chunks, err := parser.feed(": keep-alive")

	// Then
	require.NoError(t, err)
	assert.Empty(t, chunks, "a comment frame carries no data")
}

func Test_streamParser_accumulates_tool_arguments_when_they_arrive_in_fragments(t *testing.T) {
	// Given
	parser := newStreamParser()
	fragments := []string{`{"tar`, `get":"exa`, `mple`, `.com"`, `}`}

	start := `data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_nmap","arguments":""}}]},"finish_reason":null}]}`
	chunks, err := parser.feed(start)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	for _, fragment := range fragments {
		encoded, marshalErr := json.Marshal(fragment)
		require.NoError(t, marshalErr)

		frame := `data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + string(encoded) + `}}]},"finish_reason":null}]}`
		_, err = parser.feed(frame)
		require.NoError(t, err)
	}

	_, err = parser.feed(`data: {"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	require.NoError(t, err)

	// When
	_, err = parser.feed("data: [DONE]")

	// Then
	require.NoError(t, err)

	message, err := parser.message()
	require.NoError(t, err)
	require.Len(t, message.ToolCalls, 1)
	assert.Equal(t, "call_1", message.ToolCalls[0].ID)
	assert.Equal(t, "run_nmap", message.ToolCalls[0].Name)
	assert.JSONEq(t, `{"target":"example.com"}`, string(message.ToolCalls[0].Arguments),
		"the fragments must be joined before parsing")
	assert.Equal(t, llm.FinishTools, parser.finishReason())
}

func Test_streamParser_rejects_arguments_that_never_form_valid_json(t *testing.T) {
	// Given
	parser := newStreamParser()
	_, err := parser.feed(`data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_nmap","arguments":"{broken"}}]},"finish_reason":null}]}`)
	require.NoError(t, err)

	// When
	_, err = parser.feed("data: [DONE]")

	// Then
	require.Error(t, err)

	var argsErr *ToolArgumentsError
	require.True(t, errors.As(err, &argsErr))
	assert.Equal(t, "call_1", argsErr.CallID)
}

func Test_streamParser_marks_done_only_once_when_done_arrives_twice(t *testing.T) {
	// Given
	parser := newStreamParser()
	_, err := parser.feed("data: [DONE]")
	require.NoError(t, err)

	// When
	chunks, err := parser.feed("data: [DONE]")

	// Then
	require.NoError(t, err)
	assert.Empty(t, chunks, "a repeated terminator must not emit a second done chunk")
}

func Test_Stream_forwards_every_chunk_and_returns_the_full_message_when_it_completes(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, capturedSSE)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	var received []llm.Chunk
	sink := func(chunk llm.Chunk) error {
		received = append(received, chunk)
		return nil
	}

	// When
	resp, err := client.Stream(context.Background(), simpleRequest(), sink)

	// Then
	require.NoError(t, err)
	assert.NotEmpty(t, received)

	assert.Equal(t, "Starting recon.", resp.Message.Content)
	assert.Equal(t, "The target needs a port scan.", resp.Message.Reasoning,
		"reasoning is captured separately from content")
	assert.Equal(t, "cutad/deepseek-v4.1-flash", resp.ModelServed)
	assert.Equal(t, llm.FinishStop, resp.FinishReason)
	assert.Equal(t, 42, resp.Usage.InputTokens)
	assert.Equal(t, 9, resp.Usage.OutputTokens)
}

func Test_Stream_aborts_and_returns_the_sink_error_when_the_callback_fails(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, capturedSSE)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	sinkErr := errors.New("renderer closed")
	sink := func(chunk llm.Chunk) error {
		if chunk.Kind == llm.ChunkText {
			return sinkErr
		}
		return nil
	}

	// When
	_, err := client.Stream(context.Background(), simpleRequest(), sink)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, sinkErr)
}

func Test_Stream_rejects_a_nil_sink_when_called(t *testing.T) {
	// Given
	client, err := New(Options{BaseURL: "http://localhost:1", Timeout: time.Second})
	require.NoError(t, err)

	// When
	_, err = client.Stream(context.Background(), simpleRequest(), nil)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_Stream_requests_event_stream_and_include_usage_when_streaming(t *testing.T) {
	// Given
	var body map[string]any
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]")
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	sink := func(llm.Chunk) error { return nil }

	// When
	_, err := client.Stream(context.Background(), simpleRequest(), sink)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "text/event-stream", accept)
	assert.Equal(t, true, body["stream"])

	options, ok := body["stream_options"].(map[string]any)
	require.True(t, ok, "usage must be requested on the final stream frame")
	assert.Equal(t, true, options["include_usage"])
}

func Test_streamParser_reports_unknown_finish_reason_when_absent(t *testing.T) {
	// Given
	parser := newStreamParser()

	// When
	_ = feedAll(t, parser, `data: {"model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\ndata: [DONE]")

	// Then
	assert.Equal(t, llm.FinishUnknown, parser.finishReason())
}
