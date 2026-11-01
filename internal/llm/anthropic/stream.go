package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

// wireEvent is one decoded SSE event from the Messages stream.
type wireEvent struct {
	Type    string        `json:"type"`
	Index   *int          `json:"index"`
	Message *wireResponse `json:"message"`
	Block   *wireBlock    `json:"content_block"`
	Delta   *wireDelta    `json:"delta"`
	Usage   *wireUsage    `json:"usage"`
	Error   *wireError    `json:"error"`
}

type wireDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

// pendingBlock accumulates one content block across its start/delta/stop events.
//
// startInput and deltaJSON are kept apart on purpose. content_block_start carries
// a placeholder input ({"} for a streaming tool_use) and the real payload arrives
// as input_json_delta fragments; concatenating both would produce "{}{...}" which
// is not valid JSON.
type pendingBlock struct {
	kind       string
	id         string
	name       string
	startInput string
	deltaJSON  strings.Builder
}

// eventParser consumes the Messages SSE stream one line at a time.
type eventParser struct {
	servedModel string
	content     strings.Builder
	reasoning   strings.Builder

	blocks map[int]*pendingBlock
	order  []int

	stop     llm.FinishReason
	usageVal llm.Usage
	handled  bool

	done     bool
	parsed   []llm.ToolCall
	parseErr error
}

// newEventParser returns a parser for one response.
func newEventParser() *eventParser {
	return &eventParser{
		blocks: make(map[int]*pendingBlock),
		stop:   llm.FinishUnknown,
	}
}

// feed consumes one SSE line and returns the chunks it produced.
func (p *eventParser) feed(line string) ([]llm.Chunk, error) {
	if p.done {
		return nil, nil
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, "event:") {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return nil, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return nil, nil
	}

	var event wireEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}

	switch event.Type {
	case "error":
		if event.Error != nil {
			return nil, newStatusError(0, *event.Error, 0)
		}
		return nil, fmt.Errorf("stream reported an error without detail: %w", llm.ErrRetryable)

	case "message_start":
		return nil, p.onMessageStart(event)

	case "content_block_start":
		return p.onBlockStart(event), nil

	case "content_block_delta":
		return p.onBlockDelta(event), nil

	case "content_block_stop", "ping":
		return nil, nil

	case "message_delta":
		return p.onMessageDelta(event), nil

	case "message_stop":
		return p.finalize()
	}

	return nil, nil
}
