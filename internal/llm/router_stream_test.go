package llm_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/llm"
)

// providerFunc adapts a closure into a provider so a test can express exactly
// the behavior it needs without a mocking library.
func Test_Stream_fails_over_when_the_first_target_never_produced_a_chunk(t *testing.T) {
	// Given
	var served []string
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		stream: func(context.Context, llm.Request, llm.ChunkSink) (*llm.Response, error) {
			return nil, llm.ErrRetryable
		},
	}
	secondary := providerFunc{
		kind: llm.KindOpenAICompatible,
		stream: func(_ context.Context, _ llm.Request, sink llm.ChunkSink) (*llm.Response, error) {
			require.NoError(t, sink(llm.Chunk{Kind: llm.ChunkText, Text: "hello"}))
			return okResponse("served-secondary"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{
			{Name: "primary", Provider: primary, Model: "a"},
			{Name: "secondary", Provider: secondary, Model: "b"},
		},
		Clock: fixedClock{at: 1000},
	})
	require.NoError(t, err)

	sink := func(chunk llm.Chunk) error {
		served = append(served, chunk.Text)
		return nil
	}

	// When
	result, err := router.Stream(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, sink)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []string{"hello"}, served)
	assert.Equal(t, "served-secondary", result.Response.ModelServed)
}

func Test_Stream_rejects_a_nil_sink_when_called(t *testing.T) {
	// Given
	primary := providerFunc{kind: llm.KindOpenAICompatible}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "m"}},
		Clock:   fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	_, err = router.Stream(context.Background(), llm.Request{Model: "m"}, nil)

	// Then
	require.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_Classify_marks_transient_status_retryable_when_seen(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{"rate limited", 429, "", llm.ErrRetryable},
		{"server error", 503, "", llm.ErrRetryable},
		{"gateway timeout", 504, "", llm.ErrRetryable},
		{"request timeout", 408, "", llm.ErrRetryable},
		{"network failure", 0, "", llm.ErrRetryable},
		{"bad request", 400, "", llm.ErrNotRetryable},
		{"unauthorized", 401, "", llm.ErrNotRetryable},
		{"forbidden", 403, "", llm.ErrNotRetryable},
		{"unknown model", 404, "", llm.ErrNotRetryable},
		{"unprocessable", 422, "", llm.ErrNotRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := llm.Classify(tt.status, tt.code)

			// Then
			assert.ErrorIs(t, got, tt.want)
		})
	}
}

func Test_Request_Validate_rejects_a_missing_model_when_checked(t *testing.T) {
	// Given
	req := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_Request_Validate_rejects_a_tool_without_a_schema_when_checked(t *testing.T) {
	// Given
	req := llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools:    []llm.ToolSpec{{Name: "scan"}},
	}

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, llm.ErrNotRetryable)
}

func Test_Request_Validate_accepts_a_well_formed_request_when_checked(t *testing.T) {
	// Given
	req := llm.Request{
		Model:     "m",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		MaxTokens: 100,
		Tools: []llm.ToolSpec{{
			Name:   "scan",
			Schema: []byte(`{"type":"object"}`),
		}},
	}

	// When
	err := req.Validate()

	// Then
	require.NoError(t, err)
}

func Test_RouteTarget_Validate_requires_a_provider_name_and_model(t *testing.T) {
	tests := []struct {
		name   string
		target llm.RouteTarget
	}{
		{"no provider", llm.RouteTarget{Name: "a", Model: "m"}},
		{"no name", llm.RouteTarget{Provider: providerFunc{}, Model: "m"}},
		{"no model", llm.RouteTarget{Name: "a", Provider: providerFunc{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			err := tt.target.Validate()

			// Then
			require.ErrorIs(t, err, llm.ErrNoRoutes)
		})
	}
}

func Test_Attempt_records_the_latency_when_a_target_succeeds(t *testing.T) {
	// Given
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			time.Sleep(time.Millisecond)
			return okResponse("served"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "m"}},
		Clock:   fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	result, err := router.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	require.Len(t, result.Attempts, 1)
	assert.Positive(t, result.Attempts[0].Latency)
}
