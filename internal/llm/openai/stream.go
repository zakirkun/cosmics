package openai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

// streamParser consumes Server-Sent Events one line at a time.
//
// It exists as a separate value because the protocol has three hazards that a
// naive line loop gets wrong: keep-alive comment frames, tool arguments split
// across frames, and a usage frame that arrives after the last content delta.
type streamParser struct {
	servedModel string
	content     strings.Builder
	reasoning   strings.Builder

	calls     map[int]*pendingCall
	callOrder []int

	finish    llm.FinishReason
	usageSeen llm.Usage
	haveUsage bool

	done     bool
	parsed   []llm.ToolCall
	parseErr error
}

// pendingCall accumulates one tool call across frames.
type pendingCall struct {
	id       string
	name     string
	argument strings.Builder
}

// newStreamParser returns a parser for one response.
func newStreamParser() *streamParser {
	return &streamParser{
		calls:  make(map[int]*pendingCall),
		finish: llm.FinishUnknown,
	}
}

// feed consumes one SSE line and returns the chunks it produced.
func (p *streamParser) feed(line string) ([]llm.Chunk, error) {
	if p.done {
		return nil, nil
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return nil, nil
	}

	if !strings.HasPrefix(trimmed, "data:") {
		return nil, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		return p.finalize()
	}
	if payload == "" {
		return nil, nil
	}

	var frame wireStreamFrame
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		return nil, fmt.Errorf("decode stream frame: %w", err)
	}
	if frame.Error != nil {
		return nil, newStatusError(0, *frame.Error, 0)
	}

	var chunks []llm.Chunk

	if frame.Model != "" {
		p.servedModel = frame.Model
	}

	if frame.Usage != nil {
		p.usageSeen = fromWireUsage(*frame.Usage)
		p.haveUsage = true
		usage := p.usageSeen
		chunks = append(chunks, llm.Chunk{Kind: llm.ChunkUsage, Usage: &usage})
	}

	if len(frame.Choices) == 0 {
		return chunks, nil
	}

	choice := frame.Choices[0]

	if choice.Delta.ReasoningContent != "" {
		p.reasoning.WriteString(choice.Delta.ReasoningContent)
		chunks = append(chunks, llm.Chunk{Kind: llm.ChunkReasoning, Text: choice.Delta.ReasoningContent})
	}

	if choice.Delta.Content != "" {
		p.content.WriteString(choice.Delta.Content)
		chunks = append(chunks, llm.Chunk{Kind: llm.ChunkText, Text: choice.Delta.Content})
	}

	for _, fragment := range choice.Delta.ToolCalls {
		chunks = append(chunks, p.accumulate(fragment)...)
	}

	if choice.FinishReason != "" {
		p.finish = normalizeFinish(choice.FinishReason)
	}

	return chunks, nil
}

// accumulate merges one tool-call fragment and returns a chunk describing it.
func (p *streamParser) accumulate(fragment wireToolCall) []llm.Chunk {
	index := 0
	if fragment.Index != nil {
		index = *fragment.Index
	}

	call, seen := p.calls[index]
	if !seen {
		call = &pendingCall{}
		p.calls[index] = call
		p.callOrder = append(p.callOrder, index)
	}

	if fragment.ID != "" {
		call.id = fragment.ID
	}
	if fragment.Function.Name != "" {
		call.name = fragment.Function.Name
	}
	if fragment.Function.Arguments != "" {
		call.argument.WriteString(fragment.Function.Arguments)
	}

	if fragment.ID == "" && fragment.Function.Name == "" && fragment.Function.Arguments == "" {
		return nil
	}

	return []llm.Chunk{{
		Kind:     llm.ChunkToolCall,
		ToolID:   fragment.ID,
		ToolName: fragment.Function.Name,
		ArgsFrag: fragment.Function.Arguments,
	}}
}

// finalize parses the accumulated tool arguments once and emits the terminal chunk.
func (p *streamParser) finalize() ([]llm.Chunk, error) {
	if p.done {
		return nil, nil
	}
	p.done = true

	calls, err := p.toolCalls()
	if err != nil {
		p.parseErr = err
		return nil, err
	}
	p.parsed = calls

	var finished llm.FinishReason = p.finish
	if len(calls) > 0 && finished == llm.FinishUnknown {
		finished = llm.FinishTools
	}
	p.finish = finished

	return []llm.Chunk{{Kind: llm.ChunkDone}}, nil
}

// toolCalls parses every accumulated argument string exactly once.
//
// Parsing per frame would fail: the gateway happens to send a whole arguments
// string in one frame, but other OpenAI-compatible providers split it, and a
// partial string is not valid JSON.
func (p *streamParser) toolCalls() ([]llm.ToolCall, error) {
	if len(p.callOrder) == 0 {
		return nil, nil
	}

	order := make([]int, len(p.callOrder))
	copy(order, p.callOrder)
	sort.Ints(order)

	out := make([]llm.ToolCall, 0, len(order))
	for _, index := range order {
		call := p.calls[index]
		if call.name == "" {
			return nil, fmt.Errorf("stream tool call %d has no name: %w", index, llm.ErrNotRetryable)
		}

		arguments := strings.TrimSpace(call.argument.String())
		if arguments == "" {
			arguments = "{}"
		}

		var probe any
		if err := json.Unmarshal([]byte(arguments), &probe); err != nil {
			return nil, &ToolArgumentsError{Index: index, CallID: call.id, Err: err}
		}

		out = append(out, llm.ToolCall{
			ID:        call.id,
			Name:      call.name,
			Arguments: json.RawMessage(arguments),
		})
	}
	return out, nil
}

// message assembles the completed assistant turn.
func (p *streamParser) message() (llm.Message, error) {
	if p.parseErr != nil {
		return llm.Message{}, p.parseErr
	}
	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   p.content.String(),
		Reasoning: p.reasoning.String(),
		ToolCalls: p.parsed,
	}, nil
}

// finishReason reports the normalised stop reason.
func (p *streamParser) finishReason() llm.FinishReason { return p.finish }

// usage reports the token accounting seen during the stream.
func (p *streamParser) usage() llm.Usage {
	if !p.haveUsage {
		return llm.Usage{}
	}
	return p.usageSeen
}

// model reports the model the server said it served.
func (p *streamParser) model() string { return p.servedModel }
