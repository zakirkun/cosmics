package onboarding

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDiscoverer serves a fixed model list.
type stubDiscoverer struct {
	models []string
	err    error
	calls  int
}

func (s *stubDiscoverer) Models(_ context.Context, _, _ string) ([]string, error) {
	s.calls++
	return s.models, s.err
}

// runUpdate applies one message and returns the concrete model.
func runUpdate(t *testing.T, model TUI, msg tea.Msg) (TUI, tea.Cmd) {
	t.Helper()

	next, cmd := model.Update(msg)
	concrete, ok := next.(TUI)
	require.True(t, ok, "the wizard must return its own model type")
	return concrete, cmd
}

func Test_NewTUI_starts_at_the_welcome_step_when_created(t *testing.T) {
	// Given
	opts := TUIOptions{DefaultEndpoint: DefaultEndpoint}

	// When
	model := NewTUI(opts)

	// Then
	assert.Equal(t, StepWelcome, model.state.Step())
}

func Test_TUI_advances_to_the_endpoint_step_when_enter_is_pressed(t *testing.T) {
	// Given
	model := NewTUI(TUIOptions{DefaultEndpoint: DefaultEndpoint})

	// When
	model, _ = runUpdate(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Then
	assert.Equal(t, StepEndpoint, model.state.Step())
}

func Test_TUI_shows_a_discovered_list_when_the_model_step_runs_discovery(t *testing.T) {
	// Given: the model step is reached and the discoverer answers.
	discoverer := &stubDiscoverer{models: []string{"alpha", "beta"}}
	model := NewTUI(TUIOptions{DefaultEndpoint: DefaultEndpoint, Discover: discoverer})
	model.state = model.state.WithEndpoint(DefaultEndpoint)

	model, _ = runUpdate(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = runUpdate(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = runUpdate(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// When: discovery returns.
	model, _ = runUpdate(t, model, ModelsMsg{Models: discoverer.models})

	// Then
	assert.Equal(t, StepModel, model.state.Step())
	assert.Equal(t, []string{"alpha", "beta"}, model.state.Filtered())
}

func Test_TUI_filters_the_list_when_a_search_term_is_applied(t *testing.T) {
	// Given
	state := NewState(DefaultEndpoint).WithModels([]string{"alpha", "beta", "gamma"})

	// When
	filtered := state.SetSearch("ga")

	// Then
	assert.Equal(t, []string{"gamma"}, filtered.Filtered())
}

func Test_TUI_masks_the_api_key_when_rendering_it(t *testing.T) {
	// Given
	const secret = "sk-abcdefghijklmnop"

	// When
	masked := maskKey(secret)

	// Then
	assert.NotContains(t, masked, secret[:8],
		"the key must never be rendered in full")
	assert.Contains(t, masked, "mnop",
		"the last four characters may be shown to confirm the paste")
	assert.Equal(t, len([]rune(secret)), len([]rune(masked)),
		"masking must preserve the width so the layout does not shift")
}

func Test_TUI_never_writes_the_api_key_into_the_built_config_when_saving(t *testing.T) {
	// Given: a completed wizard with a key in memory.
	const secret = "sk-should-never-be-persisted"

	state := NewState(DefaultEndpoint).
		WithEndpoint(DefaultEndpoint).
		WithAPIKey(secret).
		WithModels([]string{"cutad/deepseek-v4.1-flash"}).
		Choose("cutad/deepseek-v4.1-flash")

	// When
	cfg, err := state.BuildConfig()

	// Then
	require.NoError(t, err)

	provider := cfg.Providers["9router"]
	assert.Equal(t, DefaultAPIKeyEnv, provider.APIKeyEnv,
		"only the variable name is recorded")
	assert.NotEqual(t, secret, provider.APIKeyEnv)
}

func Test_TUI_quits_without_saving_when_aborted(t *testing.T) {
	// Given
	model := NewTUI(TUIOptions{DefaultEndpoint: DefaultEndpoint})

	// When: esc schedules the abort message.
	model, cmd := runUpdate(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd, "aborting must schedule a message")

	abortMsg := cmd()
	model, quitCmd := runUpdate(t, model, abortMsg)

	// Then
	assert.True(t, model.quitting, "the abort message must end the session")
	require.NotNil(t, quitCmd, "aborting must ask the program to quit")
	_, isQuit := quitCmd().(tea.QuitMsg)
	assert.True(t, isQuit)
}

func Test_window_centres_on_the_selection_when_the_list_is_longer_than_the_view(t *testing.T) {
	// Given / When
	start, end := window(20, 10, 8)

	// Then
	assert.LessOrEqual(t, start, 10)
	assert.Greater(t, end, 10)
	assert.Equal(t, 8, end-start)
}

func Test_window_returns_the_whole_list_when_it_fits(t *testing.T) {
	// Given / When
	start, end := window(3, 1, 8)

	// Then
	assert.Equal(t, 0, start)
	assert.Equal(t, 3, end)
}

func Test_clampWidth_bounds_a_width_when_outside_the_range(t *testing.T) {
	assert.Equal(t, 40, clampWidth(10, 40, 100))
	assert.Equal(t, 100, clampWidth(200, 40, 100))
	assert.Equal(t, 72, clampWidth(72, 40, 100))
}
