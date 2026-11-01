// Package llm defines the provider-neutral contract every language model client
// implements. Concrete providers live in subpackages; nothing here performs I/O.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrUnsupported is returned when a provider cannot honour a request field.
var ErrUnsupported = errors.New("llm: unsupported by provider")

// ErrRetryable marks a failure worth retrying or failing over.
var ErrRetryable = errors.New("llm: retryable failure")

// ErrNotRetryable marks a permanent failure such as a malformed request.
var ErrNotRetryable = errors.New("llm: permanent failure")

// ErrChainExhausted is returned when every model in a fallback chain failed.
var ErrChainExhausted = errors.New("llm: fallback chain exhausted")

// ProviderKind identifies the wire protocol an endpoint speaks.
type ProviderKind string

// Supported provider kinds. The string values match the llm_calls CHECK constraint.
const (
	KindOpenAICompatible ProviderKind = "openai_compatible"
	KindAnthropic        ProviderKind = "anthropic"
)

// Valid reports whether the kind is declared.
func (k ProviderKind) Valid() bool {
	switch k {
	case KindOpenAICompatible, KindAnthropic:
		return true
	}
	return false
}

// Role is the author of a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Valid reports whether the role is declared.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleTool:
		return true
	}
	return false
}

// FinishReason explains why generation stopped.
type FinishReason string

// Finish reasons, normalised across providers.
const (
	FinishStop    FinishReason = "stop"
	FinishTools   FinishReason = "tool_calls"
	FinishLength  FinishReason = "length"
	FinishContent FinishReason = "content_filter"
	FinishUnknown FinishReason = "unknown"
)

// Usage reports token consumption for one call.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	CachedTokens    int
	ReasoningTokens int
}

// Total returns the sum of input and output tokens.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// ToolSpec is one function exposed to the model.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolCall is one invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Message is one turn in a conversation, normalised across providers.
type Message struct {
	Role      Role
	Content   string
	Reasoning string
	ToolCalls []ToolCall
	ToolID    string
}

// Request is a single completion request.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolSpec
	ToolChoice  string
	Temperature *float64
	MaxTokens   int
	Streamed    bool
}

// Validate rejects a request that cannot be sent.
func (r Request) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("request model: %w", ErrNotRetryable)
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("request max_tokens %d must not be negative: %w", r.MaxTokens, ErrNotRetryable)
	}
	for i, msg := range r.Messages {
		if !msg.Role.Valid() {
			return fmt.Errorf("request messages[%d] role %q: %w", i, msg.Role, ErrNotRetryable)
		}
	}
	for i, tool := range r.Tools {
		if tool.Name == "" {
			return fmt.Errorf("request tools[%d] name is empty: %w", i, ErrNotRetryable)
		}
		if len(tool.Schema) == 0 {
			return fmt.Errorf("request tools[%d] schema is empty: %w", i, ErrNotRetryable)
		}
	}
	return nil
}

// Response is the completed result of a call.
type Response struct {
	Message      Message
	FinishReason FinishReason
	Usage        Usage
	ModelServed  string
	Latency      time.Duration
	Attempts     int
}

// ChunkKind discriminates a stream event.
type ChunkKind string

// Stream chunk kinds.
const (
	ChunkText      ChunkKind = "text"
	ChunkReasoning ChunkKind = "reasoning"
	ChunkToolCall  ChunkKind = "tool_call"
	ChunkUsage     ChunkKind = "usage"
	ChunkDone      ChunkKind = "done"
)

// Chunk is one incremental update from a streaming call.
type Chunk struct {
	Kind     ChunkKind
	Text     string
	ToolID   string
	ToolName string
	ArgsFrag string
	Usage    *Usage
}

// ChunkSink receives stream chunks. Returning an error aborts the call, which the
// caller sees as a cancelled request.
type ChunkSink func(Chunk) error

// Provider is one model endpoint.
type Provider interface {
	Kind() ProviderKind
	Complete(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request, sink ChunkSink) (*Response, error)
}

// CallRecord is the auditable outcome of one attempt. Every field maps to a
// column on llm_calls.
type CallRecord struct {
	ID             domain.ID
	EngagementID   domain.ID
	SessionID      domain.ID
	MessageID      domain.ID
	Stage          domain.StageKind
	Provider       ProviderKind
	ModelRequested string
	ModelServed    string
	FallbackIndex  int
	Purpose        Purpose
	Status         string
	HTTPStatus     int
	ErrorCode      string
	ErrorMessage   string
	Usage          Usage
	CostUSDMicros  int64
	Latency        time.Duration
	Streamed       bool
	StartedAt      domain.TimestampMs
	FinishedAt     domain.TimestampMs
}

// Recorder persists call records. Implemented by store.LLMCallRepo.
type Recorder interface {
	Record(ctx context.Context, rec CallRecord, requestJSON, responseJSON string) error
}

// Purpose of a call, matching the llm_calls CHECK constraint.
type Purpose string

// Call purposes.
const (
	PurposeStagePlan   Purpose = "stage_plan"
	PurposeToolSelect  Purpose = "tool_select"
	PurposeOutputParse Purpose = "output_parse"
	PurposeSummarize   Purpose = "summarize"
	PurposeReportWrite Purpose = "report_write"
	PurposeCompact     Purpose = "compact"
)

// Valid reports whether the purpose is declared.
func (p Purpose) Valid() bool {
	switch p {
	case PurposeStagePlan, PurposeToolSelect, PurposeOutputParse,
		PurposeSummarize, PurposeReportWrite, PurposeCompact:
		return true
	}
	return false
}

// Classify maps an HTTP status and optional provider error code onto a retry
// decision. The rules were established by probing the live gateway: a malformed
// body or an unknown model never becomes valid on retry, while upstream account
// exhaustion does.
func Classify(httpStatus int, code string) error {
	switch {
	case httpStatus == 0:
		return ErrRetryable
	case httpStatus == 408, httpStatus == 429:
		return ErrRetryable
	case httpStatus >= 500:
		return ErrRetryable
	case httpStatus == 400, httpStatus == 401, httpStatus == 403, httpStatus == 404, httpStatus == 422:
		return ErrNotRetryable
	}

	switch code {
	case "bad_request", "invalid_request_error":
		return ErrNotRetryable
	}
	return ErrRetryable
}
