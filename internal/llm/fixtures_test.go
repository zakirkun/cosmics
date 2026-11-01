package llm_test

import (
	"context"

	"github.com/cosmics/cosmics/internal/llm"
)

// providerFunc adapts a closure into a provider so a test can express exactly
// the behavior it needs without a mocking library.
type providerFunc struct {
	kind     llm.ProviderKind
	complete func(ctx context.Context, req llm.Request) (*llm.Response, error)
	stream   func(ctx context.Context, req llm.Request, sink llm.ChunkSink) (*llm.Response, error)
}

func (p providerFunc) Kind() llm.ProviderKind { return p.kind }

func (p providerFunc) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return p.complete(ctx, req)
}

func (p providerFunc) Stream(ctx context.Context, req llm.Request, sink llm.ChunkSink) (*llm.Response, error) {
	if p.stream == nil {
		return nil, llm.ErrUnsupported
	}
	return p.stream(ctx, req, sink)
}

// fixedClock returns a constant instant.
type fixedClock struct{ at llm.Timestamp }

func (c fixedClock) Now() llm.Timestamp { return c.at }

// recordingRecorder captures every call record the router persists.
type recordingRecorder struct {
	records []llm.CallRecord
	err     error
}

func (r *recordingRecorder) Record(_ context.Context, rec llm.CallRecord, _, _ string) error {
	r.records = append(r.records, rec)
	return r.err
}

// okResponse builds a successful response naming its served model.
func okResponse(served string) *llm.Response {
	return &llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		FinishReason: llm.FinishStop,
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 2},
		ModelServed:  served,
	}
}
