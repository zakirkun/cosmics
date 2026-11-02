package onboarding_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/onboarding"
)

func Test_NewState_starts_at_welcome_with_the_default_endpoint(t *testing.T) {
	// Given / When
	state := onboarding.NewState(onboarding.DefaultEndpoint)

	// Then
	assert.Equal(t, onboarding.StepWelcome, state.Step())
	assert.Equal(t, "http://localhost:20128/v1", state.Endpoint())
}

func Test_NewState_seeds_the_default_endpoint_when_given_none(t *testing.T) {
	// Given / When
	state := onboarding.NewState("")

	// Then
	assert.Equal(t, onboarding.DefaultEndpoint, state.Endpoint())
}

func Test_Next_advances_through_every_step_in_order_when_inputs_are_valid(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)

	// When
	state, err := state.Next()
	// Then
	require.NoError(t, err)
	assert.Equal(t, onboarding.StepEndpoint, state.Step(), "welcome advances to endpoint")

	// When
	state, err = state.Next()
	// Then
	require.NoError(t, err)
	assert.Equal(t, onboarding.StepAPIKey, state.Step(), "endpoint advances to api key")

	// When
	state, err = state.Next()
	// Then
	require.NoError(t, err)
	assert.Equal(t, onboarding.StepModel, state.Step(), "api key advances to model")

	// When
	state = state.Choose("astral-projection")
	state, err = state.Next()
	// Then
	require.NoError(t, err)
	assert.Equal(t, onboarding.StepConfirm, state.Step(), "a chosen model advances to confirm")

	// When
	state, err = state.Next()
	// Then
	require.NoError(t, err)
	assert.Equal(t, onboarding.StepDone, state.Step(), "confirm advances to done")
}

func Test_Next_rejects_an_invalid_endpoint_when_advancing(t *testing.T) {
	// Given
	state, err := onboarding.NewState(onboarding.DefaultEndpoint).Next()
	require.NoError(t, err)
	state = state.WithEndpoint("/v1")

	// When
	_, err = state.Next()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, onboarding.ErrInvalidEndpoint)
}

func Test_Next_returns_ErrMissingModel_when_no_model_is_chosen(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)
	for range 3 {
		next, err := state.Next()
		require.NoError(t, err)
		state = next
	}

	// When
	_, err := state.Next()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, onboarding.ErrMissingModel)
}

func Test_Next_returns_ErrWizardComplete_when_already_done(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)
	state, err := state.Choose("m").Next()
	require.NoError(t, err)
	for state.Step() != onboarding.StepDone {
		state, err = state.Next()
		require.NoError(t, err)
	}

	// When
	_, err = state.Next()

	// Then
	assert.ErrorIs(t, err, onboarding.ErrWizardComplete)
}

func Test_Back_returns_to_the_previous_step_when_called(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)
	state, err := state.Next()
	require.NoError(t, err)
	require.Equal(t, onboarding.StepEndpoint, state.Step())

	// When
	back := state.Back()

	// Then
	assert.Equal(t, onboarding.StepWelcome, back.Step())
	assert.Equal(t, onboarding.StepEndpoint, state.Step(), "State is a value; Back must not mutate the original")
}

func Test_Back_stops_at_welcome_when_already_there(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)

	// When
	back := state.Back()

	// Then
	assert.Equal(t, onboarding.StepWelcome, back.Step())
}

func Test_SetSearch_filters_case_insensitively_when_a_substring_matches(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels([]string{
		"astral-projection",
		"cutad/deepseek-v4.1-flash",
		"other-model",
	})

	// When
	filtered := state.SetSearch("DEEPSEEK")

	// Then
	assert.Equal(t, []string{"cutad/deepseek-v4.1-flash"}, filtered.Filtered())
}

func Test_SetSearch_shows_every_model_when_the_query_is_empty(t *testing.T) {
	// Given
	models := []string{"a-model", "b-model"}
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels(models).SetSearch("a-model")
	require.Len(t, state.Filtered(), 1)

	// When
	restored := state.SetSearch("")

	// Then
	assert.Equal(t, models, restored.Filtered())
}

func Test_SetSearch_returns_no_models_when_nothing_matches(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels([]string{"astral-projection"})

	// When
	filtered := state.SetSearch("zzz")

	// Then
	assert.Empty(t, filtered.Filtered())
	assert.Equal(t, "", filtered.SelectedModel())
}

func Test_MoveSelection_clamps_at_both_ends_when_moved_past_them(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels([]string{"a", "b", "c"})

	// When moved up past the start
	high := state.MoveSelection(-99)
	// Then
	assert.Equal(t, 0, high.SelectedIndex(), "the cursor must not go below the first entry")

	// When moved down past the end
	low := state.MoveSelection(99)
	// Then
	assert.Equal(t, 2, low.SelectedIndex(), "the cursor must not go past the last entry")
}

func Test_MoveSelection_moves_by_delta_when_within_bounds(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels([]string{"a", "b", "c"})

	// When
	moved := state.MoveSelection(2)

	// Then
	assert.Equal(t, 2, moved.SelectedIndex())
	assert.Equal(t, "c", moved.SelectedModel())
}

// chosenState returns a state parked on the confirm step with a model chosen.
func chosenState(t *testing.T, model string) onboarding.State {
	t.Helper()

	state := onboarding.NewState(onboarding.DefaultEndpoint)
	state, err := state.Next()
	require.NoError(t, err)
	state, err = state.Next()
	require.NoError(t, err)
	state, err = state.WithAPIKey("sk-secret-value").Next()
	require.NoError(t, err)

	state, err = state.WithModels([]string{model}).Choose(model).Next()
	require.NoError(t, err)

	state, err = state.Next()
	require.NoError(t, err)
	require.Equal(t, onboarding.StepDone, state.Step())
	return state
}

func Test_BuildConfig_passes_validation_when_the_model_is_chosen(t *testing.T) {
	// Given
	state := chosenState(t, "astral-projection")

	// When
	cfg, err := state.BuildConfig()

	// Then
	require.NoError(t, err)
	require.NoError(t, cfg.Validate(), "the wizard output must satisfy every validation rule")
	assert.Contains(t, cfg.Providers, "9router")
	assert.Equal(t, config.ProviderOpenAICompatible, cfg.Providers["9router"].Kind)
	assert.Equal(t, "http://localhost:20128/v1", cfg.Providers["9router"].BaseURL)
}

func Test_BuildConfig_never_contains_the_api_key_value(t *testing.T) {
	// Given
	const key = "sk-secret-value"
	state := chosenState(t, "astral-projection")
	require.Equal(t, key, state.APIKey())

	// When
	cfg, err := state.BuildConfig()
	require.NoError(t, err)

	raw, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	// Then
	assert.Contains(t, string(raw), onboarding.DefaultAPIKeyEnv,
		"the file must name the environment variable that holds the key")
	assert.NotContains(t, string(raw), key,
		"SECURITY: the key value must never reach the saved configuration")
}

func Test_BuildConfig_assigns_the_chosen_model_to_every_stage_chain(t *testing.T) {
	// Given
	const model = "cutad/deepseek-v4.1-flash"
	state := chosenState(t, model)

	// When
	cfg, err := state.BuildConfig()

	// Then
	require.NoError(t, err)
	profile := cfg.ModelProfiles[onboarding.DefaultProfileName]
	require.NotNil(t, profile)

	required := append([]domain.StageKind{}, domain.AllStages...)
	required = append(required, config.CompactStageKey)
	for _, stage := range required {
		chain := profile[stage]
		require.NotEmpty(t, chain, "stage %s must declare a chain", stage)
		assert.Equal(t, model, chain[0].Model, "stage %s must use the chosen model", stage)
		assert.Equal(t, "9router", chain[0].Provider, "stage %s must use the wizard provider", stage)
	}
}

func Test_BuildConfig_returns_ErrMissingModel_when_none_was_chosen(t *testing.T) {
	// Given
	state := onboarding.NewState(onboarding.DefaultEndpoint)

	// When
	_, err := state.BuildConfig()

	// Then
	assert.ErrorIs(t, err, onboarding.ErrMissingModel)
}

func Test_BuildConfig_round_trips_through_yaml_when_saved(t *testing.T) {
	// Given
	state := chosenState(t, "astral-projection")

	// When
	cfg, err := state.BuildConfig()
	require.NoError(t, err)

	raw, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	var decoded config.Config
	require.NoError(t, yaml.Unmarshal(raw, &decoded))

	// Then
	require.NoError(t, decoded.Validate(), "a re-loaded file must still be valid")
	assert.Equal(t, "http://localhost:20128/v1", decoded.Providers["9router"].BaseURL)
}

func Test_BuildConfig_is_deterministic_when_called_twice(t *testing.T) {
	// Given
	state := chosenState(t, "astral-projection")

	// When
	first, err := state.BuildConfig()
	require.NoError(t, err)
	second, err := state.BuildConfig()
	require.NoError(t, err)

	// Then
	assert.Equal(t, first.ModelProfiles, second.ModelProfiles)
	assert.Equal(t, first.Providers, second.Providers)
}

func Test_NewState_does_not_share_the_caller_slice_when_models_are_installed(t *testing.T) {
	// Given
	models := []string{"a", "b"}
	state := onboarding.NewState(onboarding.DefaultEndpoint).WithModels(models)

	// When
	models[0] = "mutated"

	// Then
	assert.Equal(t, "a", state.Filtered()[0], "the state must own its list")
	assert.True(t, strings.HasPrefix(state.Filtered()[1], "b"))
}

func Test_BuildConfig_uses_the_operator_endpoint_when_it_was_overridden(t *testing.T) {
	// Given
	state := onboarding.NewState("http://gateway.internal:20128/v1")
	state, err := state.Next()
	require.NoError(t, err)
	state, err = state.Next()
	require.NoError(t, err)
	state, err = state.Next()
	require.NoError(t, err)
	state, err = state.Choose("astral-projection").Next()
	require.NoError(t, err)

	// When
	cfg, err := state.BuildConfig()

	// Then
	require.NoError(t, err)
	assert.Equal(t, "http://gateway.internal:20128/v1", cfg.Providers["9router"].BaseURL)
}
