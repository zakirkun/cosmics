package anthropic

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

// capturedEvents reproduces the Messages SSE event sequence.
const capturedEvents = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"cutad/deepseek-v4.1-flash","content":[],"stop_reason":null,"usage":{"input_tokens":120,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"The target needs a port scan."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Scanning "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"example.com."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":37}}

event: message_stop
data: {"type":"message_stop"}`

// feedAll drives a parser over a whole transcript.
func feedAll(t *testing.T, parser *eventParser, transcript string) []llm.Chunk {
	t.Helper()

	var all []llm.Chunk
	for _, line := range splitLines(transcript) {
		chunks, err := parser.feed(line)
		require.NoError(t, err)
		all = append(all, chunks...)
	}
	return all
}

// splitLines splits on newlines, keeping empty separator lines.
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

func Test_eventParser_emits_chunks_in_order_when_the_transcript_is_complete(t *testing.T) {
	// Given
	parser := newEventParser()

	// When
	chunks := feedAll(t, parser, capturedEvents)

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

func Test_Stream_returns_the_merged_message_when_the_stream_completes(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, capturedEvents)
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

	assert.Equal(t, "The target needs a port scan.", resp.Message.Reasoning)
	assert.Equal(t, "Scanning example.com.", resp.Message.Content)
	assert.Equal(t, "cutad/deepseek-v4.1-flash", resp.ModelServed)
	assert.Equal(t, llm.FinishStop, resp.FinishReason)

	assert.Equal(t, 120, resp.Usage.InputTokens, "input tokens arrive at message_start")
	assert.Equal(t, 37, resp.Usage.OutputTokens, "output tokens arrive at message_delta")
}

func Test_eventParser_accumulates_input_json_delta_across_frames_when_a_tool_is_used(t *testing.T) {
	// Given
	parser := newEventParser()
	transcript := `event: message_start
data: {"type":"message_start","message":{"model":"m","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"run_nmap","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"tar"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"get\":\"exa"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"mple.com\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}`

	// When
	_ = feedAll(t, parser, transcript)

	// Then
	message, err := parser.message()
	require.NoError(t, err)

	require.Len(t, message.ToolCalls, 1, "the accumulated input must produce exactly one call")
	assert.Equal(t, "toolu_1", message.ToolCalls[0].ID)
	assert.Equal(t, "run_nmap", message.ToolCalls[0].Name)
	assert.JSONEq(t, `{"target":"example.com"}`, string(message.ToolCalls[0].Arguments))
	assert.Equal(t, llm.FinishTools, parser.stopReason())
}

func Test_eventParser_keeps_two_tool_blocks_separate_when_both_stream_in_parallel(t *testing.T) {
	// Given
	parser := newEventParser()
	transcript := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"run_nmap","input":{}}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_b","name":"run_httpx","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"target\":\"b.test\"}"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"target\":\"a.test\"}"}}

event: message_stop
data: {"type":"message_stop"}`

	// When
	_ = feedAll(t, parser, transcript)

	// Then
	message, err := parser.message()
	require.NoError(t, err)
	require.Len(t, message.ToolCalls, 2)

	assert.Equal(t, "run_nmap", message.ToolCalls[0].Name)
	assert.JSONEq(t, `{"target":"a.test"}`, string(message.ToolCalls[0].Arguments))
	assert.Equal(t, "run_httpx", message.ToolCalls[1].Name)
	assert.JSONEq(t, `{"target":"b.test"}`, string(message.ToolCalls[1].Arguments))
}

func Test_eventParser_returns_a_typed_error_when_tool_input_streams_invalid_json(t *testing.T) {
	// Given
	parser := newEventParser()
	_, err := parser.feed(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"run_nmap","input":{}}}`)
	require.NoError(t, err)
	_, err = parser.feed(`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{broken"}}`)
	require.NoError(t, err)

	// When
	_, err = parser.feed(`data: {"type":"message_stop"}`)

	// Then
	require.Error(t, err)

	var inputErr *ToolInputError
	require.True(t, errors.As(err, &inputErr))
	assert.Equal(t, "toolu_1", inputErr.BlockID)
}

func Test_eventParser_ignores_keep_alive_and_event_name_lines(t *testing.T) {
	// Given
	parser := newEventParser()

	// When
	ping, errPing := parser.feed(": keep-alive")
	eventLine, errEvent := parser.feed("event: content_block_delta")

	// Then
	require.NoError(t, errPing)
	require.NoError(t, errEvent)
	assert.Empty(t, ping)
	assert.Empty(t, eventLine, "the event name is redundant: the data payload carries the type")
}

func Test_Stream_requests_event_stream_when_called(t *testing.T) {
	// Given
	var body map[string]any
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = captureBody(t, r)
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}")
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
}

func Test_Stream_aborts_and_returns_the_sink_error_when_the_callback_fails(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, capturedEvents)
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
	client, err := New(Options{BaseURL: "https://api.anthropic.com/v1", Timeout: time.Second})
	require.NoError(t, err)

	// When
	_, err = client.Stream(context.Background(), simpleRequest(), nil)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_Stream_surfaces_an_error_event_when_the_provider_reports_one(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	sink := func(llm.Chunk) error { return nil }

	// When
	_, err := client.Stream(context.Background(), simpleRequest(), sink)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrRetryable, "an overloaded provider is worth retrying")
}

func Test_eventParser_partial_json_delta_produces_a_tool_call_chunk_when_it_arrives(t *testing.T) {
	// Given
	parser := newEventParser()
	_, err := parser.feed(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"run_nmap","input":{}}}`)
	require.NoError(t, err)

	// When
	chunks, err := parser.feed(`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`)

	// Then
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, llm.ChunkToolCall, chunks[0].Kind)
	assert.Equal(t, "toolu_1", chunks[0].ToolID)
	assert.Equal(t, `{"a":1}`, chunks[0].ArgsFrag)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(chunks[0].ArgsFrag), &decoded))
	assert.InDelta(t, 1, decoded["a"], 0)
}
