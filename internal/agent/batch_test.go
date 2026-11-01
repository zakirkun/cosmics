package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/agent"
	"github.com/cosmics/cosmics/internal/llm"
)

// scriptedModel replays a fixed sequence of responses.
func Test_ExecuteBatch_returns_one_result_per_call_when_some_are_invalid(t *testing.T) {
	// Given
	tools := &recordingTools{output: "ok"}
	loop, err := agent.New(agent.Deps{
		Model:  &scriptedModel{},
		Tools:  tools,
		Exit:   &fixedExit{met: true},
		Limits: agent.DefaultLimits(),
	})
	require.NoError(t, err)

	calls := []llm.ToolCall{
		{ID: "good", Name: "run_nmap", Arguments: json.RawMessage(`{"target":"x"}`)},
		{ID: "bad", Name: "run_nmap", Arguments: json.RawMessage(`{not json`)},
		{ID: "empty", Name: "", Arguments: json.RawMessage(`{}`)},
	}

	// When
	results := loop.ExecuteBatch(context.Background(), calls)

	// Then
	require.Len(t, results, 3)
	require.NoError(t, results[0].Err)
	require.Error(t, results[1].Err, "malformed arguments must be rejected")
	require.Error(t, results[2].Err, "a nameless call must be rejected")
	assert.Equal(t, 1, tools.seenCount(), "only the valid call reaches the invoker")
}

func Test_JoinResults_combines_every_error_when_multiple_failed(t *testing.T) {
	// Given
	first := errors.New("first")
	second := errors.New("second")
	results := []agent.Result{
		{Err: first},
		{},
		{Err: second},
	}

	// When
	got := agent.JoinResults(results)

	// Then
	require.ErrorIs(t, got, first)
	require.ErrorIs(t, got, second)
}

func Test_JoinResults_returns_nil_when_no_result_failed(t *testing.T) {
	// Given
	results := []agent.Result{{Output: "ok"}, {Output: "ok"}}

	// When
	got := agent.JoinResults(results)

	// Then
	require.NoError(t, got)
}
