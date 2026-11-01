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
func Test_Run_executes_independent_tools_concurrently_when_several_are_requested(t *testing.T) {
	// Given
	tools := &recordingTools{
		output:  "scan output",
		block:   make(chan struct{}),
		entered: make(chan struct{}),
	}
	model := &scriptedModel{responses: []llm.Response{
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "a", Name: "run_nmap", Arguments: json.RawMessage(`{"target":"a.test"}`)},
					{ID: "b", Name: "run_nmap", Arguments: json.RawMessage(`{"target":"b.test"}`)},
				},
			},
			FinishReason: llm.FinishTools,
		},
		toolCallBody("done", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{
		Model:  model,
		Tools:  tools,
		Exit:   &fixedExit{met: true},
		Limits: agent.DefaultLimits(),
	})
	require.NoError(t, err)

	// When: release only after BOTH invocations have started, which is only
	// possible if they run concurrently.
	go func() {
		<-tools.entered
		close(tools.block)
	}()
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, tools.seenCount())
}

func Test_Run_stops_when_consecutive_tool_errors_reach_the_cap(t *testing.T) {
	// Given
	limits := agent.DefaultLimits()
	limits.MaxToolErrors = 2

	tools := &recordingTools{err: errors.New("container exited 137")}
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("a", "run_nmap", "{}"),
		toolCallBody("b", "run_nmap", "{}"),
		toolCallBody("c", "run_nmap", "{}"),
	}}
	loop, err := agent.New(agent.Deps{Model: model, Tools: tools, Exit: &fixedExit{met: true}, Limits: limits})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.ErrorIs(t, err, agent.ErrToolLoop)
}

func Test_Run_resets_the_error_counter_when_a_tool_succeeds(t *testing.T) {
	// Given
	limits := agent.DefaultLimits()
	limits.MaxToolErrors = 2

	tools := &alternatingTools{}
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("a", "run_nmap", "{}"),
		toolCallBody("b", "run_nmap", "{}"),
		toolCallBody("c", "run_nmap", "{}"),
		toolCallBody("d", "run_nmap", "{}"),
		toolCallBody("e", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{Model: model, Tools: tools, Exit: &fixedExit{met: true}, Limits: limits})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err, "a success between failures must reset the counter")
	assert.Equal(t, 4, tools.calls)
}

// alternatingTools fails, succeeds, fails, succeeds.
type alternatingTools struct {
	calls int
}

func (t *alternatingTools) Invoke(context.Context, string, json.RawMessage, string) (string, error) {
	t.calls++
	if t.calls%2 == 1 {
		return "", errors.New("transient failure")
	}
	return "ok", nil
}

func Test_Run_returns_the_iteration_cap_when_the_model_never_finishes(t *testing.T) {
	// Given
	limits := agent.DefaultLimits()
	limits.MaxIterations = 3

	tools := &recordingTools{output: "ok"}
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("a", "run_nmap", "{}"),
		toolCallBody("b", "run_nmap", "{}"),
		toolCallBody("c", "run_nmap", "{}"),
		toolCallBody("d", "run_nmap", "{}"),
	}}
	loop, err := agent.New(agent.Deps{Model: model, Tools: tools, Exit: &fixedExit{met: false}, Limits: limits})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.ErrorIs(t, err, agent.ErrIterationCap)
	assert.Equal(t, 3, model.callCount())
}

func Test_Run_returns_the_model_error_when_the_provider_fails(t *testing.T) {
	// Given
	model := &scriptedModel{err: errors.New("gateway unreachable")}
	loop, err := agent.New(agent.Deps{Model: model, Exit: &fixedExit{met: true}, Limits: agent.DefaultLimits()})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway unreachable")
}

func Test_Run_stops_when_the_context_is_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	model := &scriptedModel{responses: []llm.Response{plainReply("hi")}}
	loop, err := agent.New(agent.Deps{Model: model, Exit: &fixedExit{met: true}, Limits: agent.DefaultLimits()})
	require.NoError(t, err)

	// When
	err = loop.Run(ctx, "system", nil)

	// Then
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, model.callCount())
}

func Test_Run_feed_backs_a_tool_result_with_the_matching_call_id(t *testing.T) {
	// Given
	tools := &recordingTools{output: "22/tcp open ssh"}
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("call_xyz", "run_nmap", `{"target":"example.test"}`),
		toolCallBody("done", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{
		Model:  model,
		Tools:  tools,
		Exit:   &fixedExit{met: true},
		Limits: agent.DefaultLimits(),
	})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	require.Equal(t, 2, model.callCount())

	second := model.requests[1]
	var toolMessage *llm.Message
	for i := range second.Messages {
		if second.Messages[i].Role == llm.RoleTool {
			toolMessage = &second.Messages[i]
			break
		}
	}
	require.NotNil(t, toolMessage, "the tool result must be fed back to the model")
	assert.Equal(t, "call_xyz", toolMessage.ToolID, "the result must be correlated by call id")
	assert.Equal(t, "22/tcp open ssh", toolMessage.Content)
}

func Test_Run_applies_set_fact_and_add_finding_when_requested(t *testing.T) {
	// Given
	state := &stubState{}
	model := &scriptedModel{responses: []llm.Response{
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "a", Name: "set_fact", Arguments: json.RawMessage(`{"subject":"host:1","predicate":"net.os","value":"linux"}`)},
					{ID: "b", Name: "add_finding", Arguments: json.RawMessage(`{"title":"Open SSH","severity":"low"}`)},
				},
			},
			FinishReason: llm.FinishTools,
		},
		toolCallBody("done", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{
		Model:  model,
		State:  state,
		Exit:   &fixedExit{met: true},
		Limits: agent.DefaultLimits(),
	})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	assert.Len(t, state.facts, 1)
	assert.Equal(t, 1, state.findings)
}

func Test_Run_rejects_a_tool_call_the_model_misnamed_when_no_such_tool_exists(t *testing.T) {
	// Given
	tools := &recordingTools{output: "ok", known: map[string]bool{"run_nmap": true}}
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("a", "not_a_known_tool", "{}"),
		toolCallBody("done", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{
		Model:  model,
		Tools:  tools,
		Exit:   &fixedExit{met: true},
		Limits: agent.DefaultLimits(),
	})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	require.Equal(t, 2, model.callCount())

	second := model.requests[1]
	var toolMessage *llm.Message
	for i := range second.Messages {
		if second.Messages[i].Role == llm.RoleTool {
			toolMessage = &second.Messages[i]
			break
		}
	}
	require.NotNil(t, toolMessage)
	assert.Contains(t, toolMessage.Content, "error:")
}

func Test_Run_reports_an_unmet_exit_reason_to_the_model(t *testing.T) {
	// Given
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("a", "finish_stage", "{}"),
		toolCallBody("b", "finish_stage", "{}"),
	}}
	verdicts := &sequencedExit{met: []bool{false, true}, reasons: []string{"no assets recorded", ""}}
	loop, err := agent.New(agent.Deps{Model: model, Exit: verdicts, Limits: agent.DefaultLimits()})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)

	second := model.requests[1]
	var toolMessage *llm.Message
	for i := range second.Messages {
		if second.Messages[i].Role == llm.RoleTool {
			toolMessage = &second.Messages[i]
			break
		}
	}
	require.NotNil(t, toolMessage)
	assert.Contains(t, toolMessage.Content, "no assets recorded")
}
