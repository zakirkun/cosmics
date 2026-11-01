package agent_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/agent"
	"github.com/cosmics/cosmics/internal/llm"
)

// scriptedModel replays a fixed sequence of responses.
func Test_New_rejects_a_missing_exit_predicate_when_built(t *testing.T) {
	// Given / When
	_, err := agent.New(agent.Deps{Model: &scriptedModel{}, Limits: agent.DefaultLimits()})

	// Then
	require.ErrorIs(t, err, agent.ErrInvalidAction)
}

func Test_New_rejects_a_non_positive_iteration_cap_when_built(t *testing.T) {
	// Given
	limits := agent.DefaultLimits()
	limits.MaxIterations = 0

	// When
	_, err := agent.New(agent.Deps{
		Model:  &scriptedModel{},
		Exit:   &fixedExit{met: true},
		Limits: limits,
	})

	// Then
	require.ErrorIs(t, err, agent.ErrInvalidAction)
}

func Test_Run_ends_the_stage_when_the_predicate_agrees_with_finish_stage(t *testing.T) {
	// Given
	model := &scriptedModel{responses: []llm.Response{toolCallBody("call_1", "finish_stage", "{}")}}
	exit := &fixedExit{met: true}
	loop, err := agent.New(agent.Deps{Model: model, Exit: exit, Limits: agent.DefaultLimits()})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, exit.calls, "the predicate must be consulted, not trusted")
}

func Test_Run_continues_when_finish_stage_is_called_but_criteria_are_unmet(t *testing.T) {
	// Given
	model := &scriptedModel{responses: []llm.Response{
		toolCallBody("call_1", "finish_stage", "{}"),
		toolCallBody("call_2", "finish_stage", "{}"),
	}}
	verdicts := &sequencedExit{met: []bool{false, true}, reasons: []string{"no assets recorded", ""}}
	loop, err := agent.New(agent.Deps{Model: model, Exit: verdicts, Limits: agent.DefaultLimits()})
	require.NoError(t, err)

	// When
	err = loop.Run(context.Background(), "system", nil)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, verdicts.calls, "an unmet verdict must not end the stage")
	assert.Equal(t, 2, model.callCount())
}

// sequencedExit returns a scripted series of verdicts.
type sequencedExit struct {
	mu      sync.Mutex
	met     []bool
	reasons []string
	calls   int
}

func (e *sequencedExit) Evaluate(context.Context) (bool, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	index := e.calls
	e.calls++
	if index >= len(e.met) {
		return e.met[len(e.met)-1], e.reasons[len(e.reasons)-1], nil
	}
	return e.met[index], e.reasons[index], nil
}

func Test_Run_nudges_the_model_when_it_replies_with_plain_text(t *testing.T) {
	// Given
	model := &scriptedModel{responses: []llm.Response{
		plainReply("I think reconnaissance is complete."),
		toolCallBody("call_1", "finish_stage", "{}"),
	}}
	loop, err := agent.New(agent.Deps{
		Model:  model,
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
	require.NotEmpty(t, second.Messages)
	last := second.Messages[len(second.Messages)-1]
	assert.Equal(t, llm.RoleUser, last.Role)
	assert.Contains(t, last.Content, "finish_stage", "the model must be told how to end the stage")
}
