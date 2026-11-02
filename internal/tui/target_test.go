package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/clock"
	"github.com/cosmics/cosmics/internal/domain"
)

// targetNow is the fixed instant a draft is stamped with.
const targetNow = domain.TimestampMs(1_760_000_000_000)

// newTargetModel returns a shell on the target prompt with an injected clock.
func newTargetModel() Model {
	model := New(Options{Clock: clock.NewFixed(targetNow), Version: "test"})
	model.screen = ScreenNewEngagement
	model.target = NewTargetInput()
	return model
}

func Test_TargetInput_Parse_builds_one_target_when_given_one_host(t *testing.T) {
	// Given
	input := NewTargetInput().WithValue("portal.acme.test")

	// When
	engagement, targets, err := input.Parse(targetNow)

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "portal.acme.test", targets[0].Value)
	assert.Equal(t, "portal.acme.test", engagement.Name,
		"the name is derived from the target so the list is readable")
}

func Test_TargetInput_Parse_builds_several_targets_when_given_a_comma_list(t *testing.T) {
	// Given
	input := NewTargetInput().WithValue("portal.acme.test, api.acme.test")

	// When
	engagement, targets, err := input.Parse(targetNow)

	// Then
	require.NoError(t, err)
	assert.Len(t, targets, 2)
	assert.Contains(t, engagement.Name, "+1",
		"the derived name must say how many extra targets there are")
}

func Test_TargetInput_Parse_builds_several_targets_when_given_a_space_list(t *testing.T) {
	// Given
	input := NewTargetInput().WithValue("portal.acme.test api.acme.test")

	// When
	_, targets, err := input.Parse(targetNow)

	// Then
	require.NoError(t, err)
	assert.Len(t, targets, 2)
}

func Test_TargetInput_Parse_fails_when_the_prompt_is_empty(t *testing.T) {
	// Given
	input := NewTargetInput()

	// When
	_, _, err := input.Parse(targetNow)

	// Then
	require.ErrorIs(t, err, ErrInvalidTarget)
	assert.Contains(t, err.Error(), "type a target")
}

func Test_TargetInput_Parse_rejects_a_shell_metacharacter_when_given_one(t *testing.T) {
	// Given
	input := NewTargetInput().WithValue("portal.acme.test; rm -rf /")

	// When
	_, _, err := input.Parse(targetNow)

	// Then
	require.ErrorIs(t, err, ErrInvalidTarget)
}

func Test_TargetInput_Parse_defaults_to_recon_when_built_from_a_target(t *testing.T) {
	// Given
	input := NewTargetInput().WithValue("portal.acme.test")

	// When
	engagement, _, err := input.Parse(targetNow)

	// Then
	require.NoError(t, err)
	assert.Equal(t, domain.ModeRecon, engagement.Mode,
		"typing a target must not silently authorize exploitation")
}

func Test_handleTargetKey_appends_characters_when_typing(t *testing.T) {
	// Given
	model := newTargetModel()

	// When
	for _, char := range []string{"a", "c", "m", "e"} {
		model, _ = model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: []rune(char)[0], Text: char}))
	}

	// Then
	assert.Equal(t, "acme", model.target.Value())
}

func Test_handleTargetKey_does_not_navigate_when_typing_n(t *testing.T) {
	// Given
	model := newTargetModel()

	// When
	model, _ = model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))

	// Then
	assert.Equal(t, ScreenNewEngagement, model.screen,
		"typing n in the target field must not jump to the form")
	assert.Equal(t, "n", model.target.Value())
}

func Test_handleTargetKey_backspaces_one_rune_when_pressed(t *testing.T) {
	// Given
	model := newTargetModel()
	model.target = NewTargetInput().WithValue("acme")

	// When
	model, _ = model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))

	// Then
	assert.Equal(t, "acm", model.target.Value())
}

func Test_handleTargetKey_cancels_to_the_list_when_esc_is_pressed(t *testing.T) {
	// Given
	model := newTargetModel()

	// When
	model, _ = model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	// Then
	assert.Equal(t, ScreenEngagements, model.screen)
}

func Test_handleTargetKey_reports_an_error_when_enter_is_pressed_on_an_empty_prompt(t *testing.T) {
	// Given
	model := newTargetModel()

	// When
	model, cmd := model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Then
	assert.Equal(t, ScreenNewEngagement, model.screen,
		"an empty prompt must keep the operator on the prompt")
	require.Error(t, model.target.Err())
	assert.Nil(t, cmd)
}

func Test_handleTargetKey_submits_when_enter_is_pressed_on_a_valid_target(t *testing.T) {
	// Given
	model := newTargetModel()
	model.target = NewTargetInput().WithValue("portal.acme.test")

	// When
	model, cmd := model.handleTargetKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Then
	assert.Equal(t, ScreenDashboard, model.screen,
		"a valid target must go straight to the run, not back to a list")
	require.NotNil(t, cmd, "a valid target must schedule the run")

	msg, ok := cmd().(EngagementDraftedMsg)
	require.True(t, ok, "without a runner the command carries the drafted engagement")
	assert.Equal(t, "portal.acme.test", msg.Engagement.Name)
}

func Test_handleTargetPaste_inserts_the_whole_clip_when_given_one(t *testing.T) {
	// Given
	model := newTargetModel()

	// When
	model, _ = model.handleTargetPaste("portal.acme.test")

	// Then
	assert.Equal(t, "portal.acme.test", model.target.Value(),
		"a paste must insert the whole string, not drop it")
}

func Test_targetBody_shows_the_typed_value_and_the_hint_when_rendered(t *testing.T) {
	// Given
	model := newTargetModel()
	model.target = NewTargetInput().WithValue("portal.acme.test")

	// When
	rendered := model.targetBody()

	// Then
	assert.Contains(t, rendered, "portal.acme.test")
	assert.Contains(t, rendered, "enter start")
}
