package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
)

// providerFunc adapts a closure into a provider so a test can express exactly
// the behavior it needs without a mocking library.
func Test_NewRouter_rejects_an_empty_chain_when_built(t *testing.T) {
	// Given / When
	_, err := llm.NewRouter(llm.RouterOptions{})

	// Then
	require.ErrorIs(t, err, llm.ErrNoRoutes)
}

func Test_NewRouter_rejects_a_target_without_a_model_when_built(t *testing.T) {
	// Given
	target := llm.RouteTarget{
		Name:     "primary",
		Provider: providerFunc{kind: llm.KindOpenAICompatible},
	}

	// When
	_, err := llm.NewRouter(llm.RouterOptions{Targets: []llm.RouteTarget{target}})

	// Then
	require.ErrorIs(t, err, llm.ErrNoRoutes)
}

func Test_Complete_returns_the_first_target_response_when_it_succeeds(t *testing.T) {
	// Given
	calls := 0
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			calls++
			return okResponse("served-primary"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "model-a"}},
		Clock:   fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	result, err := router.Complete(context.Background(), llm.Request{
		Model:    "requested",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	require.NotNil(t, result.Response)
	assert.Equal(t, "served-primary", result.Response.ModelServed)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, result.Response.Attempts)
}

func Test_Complete_fails_over_when_the_first_target_is_retryable(t *testing.T) {
	// Given
	secondaryCalls := 0
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, llm.ErrRetryable
		},
	}
	secondary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			secondaryCalls++
			return okResponse("served-secondary"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{
			{Name: "primary", Provider: primary, Model: "model-a"},
			{Name: "secondary", Provider: secondary, Model: "model-b"},
		},
		Clock: fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	result, err := router.Complete(context.Background(), llm.Request{
		Model:    "requested",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, "served-secondary", result.Response.ModelServed)
	assert.Equal(t, 1, secondaryCalls)
	require.Len(t, result.Attempts, 2)
	assert.True(t, result.Attempts[0].Retryable, "the first attempt failed retryably")
	assert.Equal(t, 2, result.Response.Attempts, "the response records how many targets were tried")
}

func Test_Complete_stops_immediately_when_the_error_is_permanent(t *testing.T) {
	// Given
	secondaryCalls := 0
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, llm.ErrNotRetryable
		},
	}
	secondary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			secondaryCalls++
			return okResponse("served-secondary"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{
			{Name: "primary", Provider: primary, Model: "model-a"},
			{Name: "secondary", Provider: secondary, Model: "model-b"},
		},
		Clock: fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	_, err = router.Complete(context.Background(), llm.Request{
		Model:    "requested",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, llm.ErrNotRetryable)
	assert.Zero(t, secondaryCalls, "a permanent failure must not try the fallback: it would hide a caller bug")
}

func Test_Complete_returns_ErrChainExhausted_when_every_target_is_retryable(t *testing.T) {
	// Given
	failing := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, llm.ErrRetryable
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{
			{Name: "a", Provider: failing, Model: "m1"},
			{Name: "b", Provider: failing, Model: "m2"},
		},
		Clock: fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	result, err := router.Complete(context.Background(), llm.Request{
		Model:    "requested",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, llm.ErrChainExhausted)
	assert.Nil(t, result.Response)
	assert.Len(t, result.Attempts, 2)
}

func Test_Complete_uses_the_target_model_and_token_budget_when_dispatching(t *testing.T) {
	// Given
	var seen llm.Request
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(_ context.Context, req llm.Request) (*llm.Response, error) {
			seen = req
			return okResponse("served"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "target-model", MaxTokens: 1234}},
		Clock:   fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	_, err = router.Complete(context.Background(), llm.Request{
		Model:     "caller-model",
		MaxTokens: 99,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, "target-model", seen.Model, "the chain owns the model identifier")
	assert.Equal(t, 1234, seen.MaxTokens, "the chain owns the completion budget")
}

func Test_Complete_stops_when_the_context_is_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			calls++
			return okResponse("served"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "m"}},
		Clock:   fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	_, err = router.Complete(ctx, llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls, "a cancelled context must not reach a provider")
}

func Test_Complete_records_each_attempt_when_a_recorder_is_configured(t *testing.T) {
	// Given
	recorder := &recordingRecorder{}
	failing := providerFunc{
		kind: llm.KindAnthropic,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return nil, llm.ErrRetryable
		},
	}
	secondary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return okResponse("served-secondary"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets: []llm.RouteTarget{
			{Name: "primary", Provider: failing, Model: "model-a"},
			{Name: "secondary", Provider: secondary, Model: "model-b"},
		},
		Recorder: recorder,
		Clock:    fixedClock{at: 1000},
	})
	require.NoError(t, err)

	ctx := llm.WithStage(context.Background(), domain.StageRecon)
	ctx = llm.WithPurpose(ctx, llm.PurposeToolSelect)

	// When
	_, err = router.Complete(ctx, llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err)
	require.Len(t, recorder.records, 2)

	assert.Equal(t, "error", recorder.records[0].Status)
	assert.Equal(t, llm.KindAnthropic, recorder.records[0].Provider)
	assert.Equal(t, domain.StageRecon, recorder.records[0].Stage)
	assert.Equal(t, llm.PurposeToolSelect, recorder.records[0].Purpose)
	assert.Equal(t, "model-a", recorder.records[0].ModelRequested)

	assert.Equal(t, "ok", recorder.records[1].Status)
	assert.Equal(t, "served-secondary", recorder.records[1].ModelServed)
	assert.Equal(t, "model-b", recorder.records[1].ModelRequested)
}

func Test_Complete_returns_the_response_when_the_recorder_fails(t *testing.T) {
	// Given
	recorder := &recordingRecorder{err: errors.New("ledger unavailable")}
	primary := providerFunc{
		kind: llm.KindOpenAICompatible,
		complete: func(context.Context, llm.Request) (*llm.Response, error) {
			return okResponse("served"), nil
		},
	}
	router, err := llm.NewRouter(llm.RouterOptions{
		Targets:  []llm.RouteTarget{{Name: "primary", Provider: primary, Model: "m"}},
		Recorder: recorder,
		Clock:    fixedClock{at: 1000},
	})
	require.NoError(t, err)

	// When
	result, err := router.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})

	// Then
	require.NoError(t, err, "a ledger failure must not fail a successful model call")
	require.NotNil(t, result.Response)
}
