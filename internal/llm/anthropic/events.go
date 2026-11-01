package anthropic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

const blockTypeToolUse = "tool_use"

// onMessageStart records the model and the initial usage counters.
func (p *eventParser) onMessageStart(event wireEvent) {
	if event.Message == nil {
		return
	}
	if event.Message.Model != "" {
		p.servedModel = event.Message.Model
	}
	p.mergeUsage(event.Message.Usage)
}

// onBlockStart registers a block and emits any content it opens with.
func (p *eventParser) onBlockStart(event wireEvent) []llm.Chunk {
	if event.Block == nil {
		return nil
	}

	index := 0
	if event.Index != nil {
		index = *event.Index
	}

	block, seen := p.blocks[index]
	if !seen {
		block = &pendingBlock{}
		p.blocks[index] = block
		p.order = append(p.order, index)
	}

	block.kind = event.Block.Type
	if event.Block.ID != "" {
		block.id = event.Block.ID
	}
	if event.Block.Name != "" {
		block.name = event.Block.Name
	}

	var chunks []llm.Chunk

	switch event.Block.Type {
	case "text":
		if event.Block.Text != "" {
			p.content.WriteString(event.Block.Text)
			chunks = append(chunks, llm.Chunk{Kind: llm.ChunkText, Text: event.Block.Text})
		}
	case "thinking":
		if event.Block.Thinking != "" {
			p.reasoning.WriteString(event.Block.Thinking)
			chunks = append(chunks, llm.Chunk{Kind: llm.ChunkReasoning, Text: event.Block.Thinking})
		}
	case blockTypeToolUse:
		if len(event.Block.Input) > 0 && string(event.Block.Input) != "{}" {
			block.startInput = string(event.Block.Input)
		}
		chunks = append(chunks, llm.Chunk{
			Kind:     llm.ChunkToolCall,
			ToolID:   block.id,
			ToolName: block.name,
		})
	}

	return chunks
}

// onBlockDelta applies one delta to its block.
func (p *eventParser) onBlockDelta(event wireEvent) []llm.Chunk {
	if event.Delta == nil {
		return nil
	}

	index := 0
	if event.Index != nil {
		index = *event.Index
	}

	block, seen := p.blocks[index]
	if !seen {
		block = &pendingBlock{}
		p.blocks[index] = block
		p.order = append(p.order, index)
	}

	switch event.Delta.Type {
	case "thinking_delta":
		p.reasoning.WriteString(event.Delta.Thinking)
		return []llm.Chunk{{Kind: llm.ChunkReasoning, Text: event.Delta.Thinking}}

	case "text_delta":
		p.content.WriteString(event.Delta.Text)
		return []llm.Chunk{{Kind: llm.ChunkText, Text: event.Delta.Text}}

	case "input_json_delta":
		block.deltaJSON.WriteString(event.Delta.PartialJSON)
		return []llm.Chunk{{
			Kind:     llm.ChunkToolCall,
			ToolID:   block.id,
			ToolName: block.name,
			ArgsFrag: event.Delta.PartialJSON,
		}}

	case "signature_delta":
		return nil
	}

	return nil
}

// onMessageDelta records the stop reason and the final usage counters.
func (p *eventParser) onMessageDelta(event wireEvent) []llm.Chunk {
	if event.Delta != nil && event.Delta.StopReason != "" {
		p.stop = normalizeStopReason(event.Delta.StopReason)
	}
	if event.Usage != nil {
		p.mergeUsage(*event.Usage)
		usage := p.usageVal
		return []llm.Chunk{{Kind: llm.ChunkUsage, Usage: &usage}}
	}
	return nil
}

// mergeUsage keeps the larger value per counter.
//
// The stream reports input tokens at message_start and output tokens at
// message_delta, so a later event must not zero a counter it does not carry.
func (p *eventParser) mergeUsage(usage wireUsage) {
	if usage.InputTokens > p.usageVal.InputTokens {
		p.usageVal.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens > p.usageVal.OutputTokens {
		p.usageVal.OutputTokens = usage.OutputTokens
	}
	if usage.CacheReadInputTokens > p.usageVal.CachedTokens {
		p.usageVal.CachedTokens = usage.CacheReadInputTokens
	}
	p.handled = true
}

// finalize parses every accumulated tool input and emits the terminal chunk.
func (p *eventParser) finalize() ([]llm.Chunk, error) {
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

	if len(calls) > 0 && p.stop == llm.FinishUnknown {
		p.stop = llm.FinishTools
	}
	return []llm.Chunk{{Kind: llm.ChunkDone}}, nil
}

// toolCalls parses the accumulated input fragments once per block.
func (p *eventParser) toolCalls() ([]llm.ToolCall, error) {
	if len(p.order) == 0 {
		return nil, nil
	}

	order := make([]int, len(p.order))
	copy(order, p.order)
	sort.Ints(order)

	out := make([]llm.ToolCall, 0, len(order))
	for _, index := range order {
		block := p.blocks[index]
		if block.kind != blockTypeToolUse {
			continue
		}
		if block.name == "" {
			return nil, fmt.Errorf("tool_use block %d has no name: %w", index, llm.ErrNotRetryable)
		}

		input := strings.TrimSpace(block.deltaJSON.String())
		if input == "" {
			input = strings.TrimSpace(block.startInput)
		}
		if input == "" {
			input = "{}"
		}

		var probe any
		if err := json.Unmarshal([]byte(input), &probe); err != nil {
			return nil, &ToolInputError{Index: index, BlockID: block.id, Err: err}
		}

		out = append(out, llm.ToolCall{
			ID:        block.id,
			Name:      block.name,
			Arguments: json.RawMessage(input),
		})
	}
	return out, nil
}

// message assembles the completed assistant turn.
func (p *eventParser) message() (llm.Message, error) {
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

func (p *eventParser) stopReason() llm.FinishReason { return p.stop }
func (p *eventParser) model() string                { return p.servedModel }

func (p *eventParser) usage() llm.Usage {
	if !p.handled {
		return llm.Usage{}
	}
	return p.usageVal
}

// ToolInputError names the tool block whose accumulated input was not JSON.
type ToolInputError struct {
	Index   int
	BlockID string
	Err     error
}

func (e *ToolInputError) Error() string {
	return fmt.Sprintf("anthropic: tool_use block %d (%s) input is not valid JSON: %v", e.Index, e.BlockID, e.Err)
}

func (e *ToolInputError) Unwrap() error { return e.Err }
