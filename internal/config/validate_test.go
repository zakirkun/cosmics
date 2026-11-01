package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
)

// mutateDefault returns a valid config with one field changed.
func mutateDefault(apply func(*config.Config)) config.Config {
	cfg := config.Default()
	apply(&cfg)
	return cfg
}

func Test_Validate_rejects_each_invalid_field_when_present(t *testing.T) {
	tests := []struct {
		name     string
		apply    func(*config.Config)
		contains string
	}{
		{
			name: "unknown provider kind",
			apply: func(c *config.Config) {
				p := c.Providers["9router"]
				p.Kind = "not_a_kind"
				c.Providers["9router"] = p
			},
			contains: "kind",
		},
		{
			name: "relative base url",
			apply: func(c *config.Config) {
				p := c.Providers["9router"]
				p.BaseURL = "/v1"
				c.Providers["9router"] = p
			},
			contains: "base_url",
		},
		{
			name: "empty api key env",
			apply: func(c *config.Config) {
				p := c.Providers["9router"]
				p.APIKeyEnv = ""
				c.Providers["9router"] = p
			},
			contains: "api_key_env",
		},
		{
			name: "zero provider timeout",
			apply: func(c *config.Config) {
				p := c.Providers["9router"]
				p.TimeoutS = 0
				c.Providers["9router"] = p
			},
			contains: "timeout_s",
		},
		{
			name: "missing stage in profile",
			apply: func(c *config.Config) {
				delete(c.ModelProfiles["default"], "recon")
			},
			contains: "default.recon",
		},
		{
			name: "unknown provider referenced by a model entry",
			apply: func(c *config.Config) {
				profile := c.ModelProfiles["default"]
				profile["recon"][0].Provider = "ghost"
				c.ModelProfiles["default"] = profile
			},
			contains: "is not declared in providers",
		},
		{
			name: "zero tool timeout",
			apply: func(c *config.Config) {
				c.Tools.DefaultTimeoutS = 0
			},
			contains: "default_timeout_s",
		},
		{
			name: "bad retrieval mode",
			apply: func(c *config.Config) {
				c.Memory.RetrievalMode = "semantic"
			},
			contains: "retrieval_mode",
		},
		{
			name: "out of range compaction threshold",
			apply: func(c *config.Config) {
				c.Memory.CompactionThreshold = 1.5
			},
			contains: "compaction_threshold",
		},
		{
			name: "vector mode without embeddings",
			apply: func(c *config.Config) {
				c.Memory.RetrievalMode = config.RetrievalVector
			},
			contains: "embeddings",
		},
		{
			name: "bad log level",
			apply: func(c *config.Config) {
				c.App.LogLevel = "verbose"
			},
			contains: "log_level",
		},
		{
			name: "no providers declared",
			apply: func(c *config.Config) {
				c.Providers = nil
			},
			contains: "providers",
		},
		{
			name: "zero approval timeout",
			apply: func(c *config.Config) {
				c.Exploit.ApprovalTimeoutS = 0
			},
			contains: "approval_timeout_s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			cfg := mutateDefault(tt.apply)

			// When
			err := cfg.Validate()

			// Then
			require.Error(t, err)
			require.ErrorIs(t, err, config.ErrInvalidConfig)
			assert.Contains(t, err.Error(), tt.contains)
		})
	}
}

func Test_Validate_joins_all_problems_when_several_rules_break(t *testing.T) {
	// Given
	cfg := mutateDefault(func(c *config.Config) {
		c.App.LogLevel = "verbose"
		c.Tools.DefaultTimeoutS = 0
		c.Memory.TopK = 0
	})

	// When
	err := cfg.Validate()

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, config.ErrInvalidConfig)

	message := err.Error()
	assert.Contains(t, message, "log_level")
	assert.Contains(t, message, "default_timeout_s")
	assert.Contains(t, message, "top_k")
}

func Test_Validate_accepts_hybrid_mode_when_embeddings_are_configured(t *testing.T) {
	// Given
	cfg := mutateDefault(func(c *config.Config) {
		c.Memory.RetrievalMode = config.RetrievalHybrid
		c.Memory.Embeddings = config.EmbeddingsConfig{
			Enabled:    true,
			Provider:   "9router",
			Model:      "text-embedding-3-small",
			Dimensions: 1536,
		}
	})

	// When
	err := cfg.Validate()

	// Then
	require.NoError(t, err)
}

func Test_Validate_returns_one_error_per_missing_stage_when_a_profile_is_empty(t *testing.T) {
	// Given
	cfg := mutateDefault(func(c *config.Config) {
		c.ModelProfiles["default"] = config.ModelProfile{}
	})

	// When
	err := cfg.Validate()

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default.recon")
	assert.Contains(t, err.Error(), "default.reporting")
}
