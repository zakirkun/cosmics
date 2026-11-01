package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

type wireRequest struct {
	Model         string             `json:"model"`
	Messages      []wireMessage      `json:"messages"`
	Tools         []wireTool         `json:"tools,omitempty"`
	ToolChoice    string             `json:"tool_choice,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type wireTool struct {
	Type     string      `json:"type"`
	Function wireToolDef `json:"function"`
}

type wireToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireUsage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	PromptTokensDetails     wirePromptTokensDetails `json:"prompt_tokens_details"`
	CompletionTokensDetails wireCompTokensDetails   `json:"completion_tokens_details"`
}

type wirePromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type wireCompTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type wireResponse struct {
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   wireUsage    `json:"usage"`
	Error   *wireError   `json:"error,omitempty"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireStreamFrame struct {
	Model   string             `json:"model"`
	Choices []wireStreamChoice `json:"choices"`
	Usage   *wireUsage         `json:"usage,omitempty"`
	Error   *wireError         `json:"error,omitempty"`
}

type wireStreamChoice struct {
	Index        int       `json:"index"`
	Delta        wireDelta `json:"delta"`
	FinishReason string    `json:"finish_reason"`
}

type wireDelta struct {
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content"`
	ToolCalls        []wireToolCall `json:"tool_calls"`
}

type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type wireErrorEnvelope struct {
	Error wireError `json:"error"`
}

// ToolArgumentsError names the tool call whose arguments were not valid JSON.
//
// The index and call id are carried because a malformed argument string is
// otherwise indistinguishable from a malformed response, and the caller needs to
// tell the model exactly which call to correct.
type ToolArgumentsError struct {
	Index  int
	CallID string
	Err    error
}

func (e *ToolArgumentsError) Error() string {
	return fmt.Sprintf("openai: tool call %d (%s) arguments are not valid JSON: %v", e.Index, e.CallID, e.Err)
}

func (e *ToolArgumentsError) Unwrap() error { return e.Err }

func toWireRequest(req llm.Request, stream bool) wireRequest {
	wire := wireRequest{
		Model:       req.Model,
		Messages:    toWireMessages(req),
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      stream,
	}
	if len(req.Tools) > 0 {
		wire.Tools = toWireTools(req.Tools)
		if wire.ToolChoice == "" {
			wire.ToolChoice = "auto"
		}
	}
	if stream {
		wire.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	}
	return wire
}

func toWireMessages(req llm.Request) []wireMessage {
	capacity := len(req.Messages)
	if req.System != "" {
		capacity++
	}

	out := make([]wireMessage, 0, capacity)
	if req.System != "" {
		out = append(out, wireMessage{Role: "system", Content: req.System})
	}

	for _, msg := range req.Messages {
		wire := wireMessage{Role: string(msg.Role), Content: msg.Content}
		if msg.Role == llm.RoleTool {
			wire.ToolCallID = msg.ToolID
		}
		if len(msg.ToolCalls) > 0 {
			wire.ToolCalls = make([]wireToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				arguments := strings.TrimSpace(string(call.Arguments))
				if arguments == "" {
					arguments = "{}"
				}
				wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
					ID:       call.ID,
					Type:     "function",
					Function: wireFunction{Name: call.Name, Arguments: arguments},
				})
			}
		}
		out = append(out, wire)
	}
	return out
}

func toWireTools(specs []llm.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(specs))
	for _, spec := range specs {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolDef{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Schema,
			},
		})
	}
	return out
}

func fromWireUsage(usage wireUsage) llm.Usage {
	return llm.Usage{
		InputTokens:     usage.PromptTokens,
		OutputTokens:    usage.CompletionTokens,
		CachedTokens:    usage.PromptTokensDetails.CachedTokens,
		ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens,
	}
}

func fromWireMessage(msg wireMessage) (llm.Message, error) {
	calls, err := fromWireToolCalls(msg.ToolCalls)
	if err != nil {
		return llm.Message{}, err
	}
	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   msg.Content,
		Reasoning: msg.ReasoningContent,
		ToolCalls: calls,
	}, nil
}

// fromWireToolCalls decodes the arguments string into JSON once per call.
//
// Arguments arrive as a JSON *string*, not an object, and providers split that
// string across frames when streaming. Validation happens through an intermediate
// value because json.RawMessage accepts any bytes without checking them.
func fromWireToolCalls(raw []wireToolCall) ([]llm.ToolCall, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	out := make([]llm.ToolCall, 0, len(raw))
	for index, call := range raw {
		if call.Function.Name == "" {
			return nil, fmt.Errorf("openai: tool call %d (%s) has no function name: %w",
				index, call.ID, llm.ErrNotRetryable)
		}

		arguments := strings.TrimSpace(call.Function.Arguments)
		if arguments == "" {
			arguments = "{}"
		}

		var probe any
		if err := json.Unmarshal([]byte(arguments), &probe); err != nil {
			return nil, &ToolArgumentsError{Index: index, CallID: call.ID, Err: err}
		}

		out = append(out, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(arguments),
		})
	}
	return out, nil
}

func normalizeFinish(reason string) llm.FinishReason {
	switch reason {
	case "stop":
		return llm.FinishStop
	case "tool_calls", "function_call":
		return llm.FinishTools
	case "length":
		return llm.FinishLength
	case "content_filter":
		return llm.FinishContent
	case "":
		return llm.FinishUnknown
	}
	return llm.FinishUnknown
}
