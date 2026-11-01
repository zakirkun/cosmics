package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

type wireRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	System      string          `json:"system,omitempty"`
	Messages    []wireMessage   `json:"messages"`
	Tools       []wireTool      `json:"tools,omitempty"`
	ToolChoice  *wireToolChoice `json:"tool_choice,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type wireToolChoice struct {
	Type string `json:"type"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireBlock struct {
	Type string `json:"type"`

	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireCacheControl struct {
	Type string `json:"type"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     int  `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int  `json:"cache_creation_input_tokens"`
	Estimated                bool `json:"estimated"`
}

type wireResponse struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Role       string      `json:"role"`
	Model      string      `json:"model"`
	Content    []wireBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Usage      wireUsage   `json:"usage"`
	Error      *wireError  `json:"error,omitempty"`
}

type wireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireErrorEnvelope struct {
	Type  string    `json:"type"`
	Error wireError `json:"error"`
}

// toWireRequest translates a normalised request into the Messages protocol.
//
// max_tokens is mandatory upstream and defaults to 2048 when omitted, which
// truncates long outputs without an error. Sending an explicit budget is the only
// way to get the length the caller asked for.
func toWireRequest(req llm.Request, stream bool) wireRequest {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	wire := wireRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		System:      req.System,
		Messages:    toWireContent(req.Messages),
		Temperature: req.Temperature,
		Stream:      stream,
	}

	if len(req.Tools) > 0 {
		wire.Tools = toWireTools(req.Tools)
		switch req.ToolChoice {
		case "":
			wire.ToolChoice = &wireToolChoice{Type: "auto"}
		case "required":
			wire.ToolChoice = &wireToolChoice{Type: "any"}
		case "none":
			wire.ToolChoice = nil
		default:
			wire.ToolChoice = &wireToolChoice{Type: "tool"}
		}
	}

	return wire
}

// toWireContent converts normalised messages into alternating content blocks.
//
// A message that would produce zero blocks is skipped: the API rejects an empty
// content array, and an empty assistant turn carries no information anyway.
func toWireContent(messages []llm.Message) []wireMessage {
	out := make([]wireMessage, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleTool:
			payload, err := json.Marshal(msg.Content)
			if err != nil {
				continue
			}
			out = append(out, wireMessage{
				Role: "user",
				Content: []wireBlock{{
					Type:      "tool_result",
					ToolUseID: msg.ToolID,
					Content:   payload,
				}},
			})

		case llm.RoleUser:
			if msg.Content == "" {
				continue
			}
			out = append(out, wireMessage{
				Role:    "user",
				Content: []wireBlock{{Type: "text", Text: msg.Content}},
			})

		case llm.RoleAssistant:
			blocks := make([]wireBlock, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				input := strings.TrimSpace(string(call.Arguments))
				if input == "" {
					input = "{}"
				}
				blocks = append(blocks, wireBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Name,
					Input: json.RawMessage(input),
				})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})
		}
	}

	return out
}

func toWireTools(specs []llm.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(specs))
	for _, spec := range specs {
		out = append(out, wireTool{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.Schema,
		})
	}
	return out
}

// fromWireBlocks merges EVERY content block into one assistant message.
//
// Taking only the first block is the classic bug here: a response commonly leads
// with a thinking block, so a first-block-only reader returns reasoning and
// silently drops the tool_use that follows it.
func fromWireBlocks(blocks []wireBlock) (llm.Message, error) {
	message := llm.Message{Role: llm.RoleAssistant}

	var text strings.Builder
	var thinking strings.Builder

	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			message.ToolCalls = append(message.ToolCalls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: input,
			})
		case "tool_result", "redacted_thinking", "server_tool_use", "web_search_tool_result":
			// Present in some responses but carries nothing the agent acts on.
		}
	}

	message.Content = text.String()
	message.Reasoning = thinking.String()

	if len(message.ToolCalls) > 0 && message.ToolCalls[0].Name == "" {
		return llm.Message{}, fmt.Errorf("anthropic: tool_use block has no name: %w", llm.ErrNotRetryable)
	}
	return message, nil
}

func fromWireUsage(usage wireUsage) llm.Usage {
	return llm.Usage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		CachedTokens: usage.CacheReadInputTokens,
	}
}

func normalizeStopReason(reason string) llm.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return llm.FinishStop
	case "tool_use":
		return llm.FinishTools
	case "max_tokens":
		return llm.FinishLength
	case "refusal":
		return llm.FinishContent
	case "":
		return llm.FinishUnknown
	}
	return llm.FinishUnknown
}
