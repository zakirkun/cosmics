package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
)

// writeConfig writes a YAML fixture into a fresh temp directory.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// minimalConfig is the smallest YAML that validates cleanly.
const minimalConfig = `
app:
  operator: tester
  data_dir: /tmp/cosmics-test
  log_level: info

providers:
  9router:
    kind: openai_compatible
    base_url: http://localhost:20128/v1
    api_key_env: NINEROUTER_KEY
    timeout_s: 120
    max_retries: 2

tools:
  default_timeout_s: 300
  max_output_bytes: 262144

exploit:
  approval_timeout_s: 300
  max_attempts_per_path: 3

memory:
  retrieval_mode: keyword
  top_k: 8
  token_budget: 6000
  compaction_threshold: 0.75

osint:
  enabled_sources: [crtsh]
`

func Test_Default_has_a_fallback_chain_for_every_stage(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	profile := cfg.ModelProfiles["default"]

	// Then
	require.NotNil(t, profile)

	required := append(slicesClone(domain.AllStages), config.CompactStageKey)
	for _, stage := range required {
		chain := profile[stage]
		require.NotEmpty(t, chain, "stage %s must declare a chain", stage)
		require.GreaterOrEqual(t, len(chain), 2, "stage %s must declare a fallback", stage)

		for i, entry := range chain {
			assert.NotEmpty(t, entry.Provider, "stage %s entry %d provider", stage, i)
			assert.NotEmpty(t, entry.Model, "stage %s entry %d model", stage, i)
			assert.Positive(t, entry.MaxTokens, "stage %s entry %d max_tokens", stage, i)
		}
	}
}

// slicesClone avoids importing slices in the test for a single call.
func slicesClone[T any](in []T) []T {
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func Test_Default_validates_cleanly_when_used_as_shipped(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	err := cfg.Validate()

	// Then
	require.NoError(t, err)
}

func Test_Load_applies_defaults_when_optional_sections_are_omitted(t *testing.T) {
	// Given
	path := writeConfig(t, minimalConfig)

	// When
	cfg, err := config.Load(context.Background(), path)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "tester", cfg.App.Operator)
	assert.Equal(t, config.RetrievalKeyword, cfg.Memory.RetrievalMode)
	assert.Positive(t, cfg.Tools.DefaultTimeoutS)
	assert.Positive(t, cfg.Memory.TopK)
	assert.NotEmpty(t, cfg.OSINT.EnabledSources)
}

func Test_Load_expands_environment_variables_when_placeholders_are_used(t *testing.T) {
	// Given
	t.Setenv("COSMICS_TEST_OPERATOR", "env-operator")
	path := writeConfig(t, strings.Replace(minimalConfig, "operator: tester", "operator: ${COSMICS_TEST_OPERATOR}", 1))

	// When
	cfg, err := config.Load(context.Background(), path)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "env-operator", cfg.App.Operator)
}

func Test_Load_returns_an_error_when_the_file_is_missing(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "absent.yaml")

	// When
	_, err := config.Load(context.Background(), path)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func Test_Load_returns_an_error_when_the_yaml_is_malformed(t *testing.T) {
	// Given
	path := writeConfig(t, "app:\n  operator: [unclosed\n")

	// When
	_, err := config.Load(context.Background(), path)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func Test_Load_respects_a_cancelled_context(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := writeConfig(t, minimalConfig)

	// When
	_, err := config.Load(ctx, path)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func Test_LoadFromEnv_builds_a_usable_config_when_no_file_exists(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_URL", "http://localhost:20128/v1")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// When
	cfg, err := config.LoadFromEnv(context.Background())

	// Then
	require.NoError(t, err)
	require.Contains(t, cfg.Providers, "9router")
	assert.Equal(t, "http://localhost:20128/v1", cfg.Providers["9router"].BaseURL)
}

func Test_LoadFromEnv_adds_anthropic_when_its_key_is_present(t *testing.T) {
	// Given
	t.Setenv("ANTHROPIC_API_KEY", "present")

	// When
	cfg, err := config.LoadFromEnv(context.Background())

	// Then
	require.NoError(t, err)
	require.Contains(t, cfg.Providers, "anthropic")
	assert.Equal(t, config.ProviderAnthropic, cfg.Providers["anthropic"].Kind)
}

func Test_ResolveModelChain_returns_the_configured_chain_when_the_profile_exists(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	chain, err := cfg.ResolveModelChain("default", domain.StageAnalysis)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, chain)
	assert.Equal(t, "9router", chain[0].Provider)
}

func Test_ResolveModelChain_returns_ErrUnknownProfile_when_the_profile_is_absent(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	_, err := cfg.ResolveModelChain("no-such-profile", domain.StageRecon)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, config.ErrUnknownProfile)
}

func Test_ResolveModelChain_returns_ErrNoModelForStage_when_the_stage_is_absent(t *testing.T) {
	// Given
	cfg := config.Default()
	delete(cfg.ModelProfiles["default"], domain.StageReporting)

	// When
	_, err := cfg.ResolveModelChain("default", domain.StageReporting)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, config.ErrNoModelForStage)
}

func Test_DataDir_expands_a_tilde_prefix(t *testing.T) {
	// Given
	cfg := config.Default()
	cfg.App.DataDir = "~/cosmics-data"

	// When
	got, err := cfg.DataDir()

	// Then
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(got), "expected an absolute path, got %s", got)
	assert.False(t, strings.HasPrefix(got, "~"))
	assert.Contains(t, got, "cosmics-data")
}
