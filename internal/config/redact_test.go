package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
)

func Test_Redact_replaces_a_configured_secret_when_it_appears_in_text(t *testing.T) {
	// Given
	const secret = "sk-test-very-secret-value"
	t.Setenv("NINEROUTER_KEY", secret)
	cfg := config.Default()

	// When
	got := cfg.Redact("request failed with Authorization: Bearer " + secret)

	// Then
	assert.NotContains(t, got, secret)
	assert.Contains(t, got, config.RedactedPlaceholder)
}

func Test_Redact_leaves_text_unchanged_when_no_secret_is_present(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_KEY", "sk-test-very-secret-value")
	cfg := config.Default()
	const text = "nmap completed with exit code 0"

	// When
	got := cfg.Redact(text)

	// Then
	assert.Equal(t, text, got)
}

func Test_Redact_returns_empty_input_unchanged(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_KEY", "sk-test")
	cfg := config.Default()

	// When
	got := cfg.Redact("")

	// Then
	assert.Empty(t, got)
}

func Test_Redact_does_not_replace_everything_when_the_secret_is_unset(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_KEY", "")
	cfg := config.Default()
	const text = "scan finished"

	// When
	got := cfg.Redact(text)

	// Then
	assert.Equal(t, text, got)
}

func Test_Redact_replaces_every_occurrence_when_the_secret_repeats(t *testing.T) {
	// Given
	const secret = "abcdef123456"
	t.Setenv("NINEROUTER_KEY", secret)
	cfg := config.Default()

	// When
	got := cfg.Redact(secret + " and again " + secret)

	// Then
	assert.NotContains(t, got, secret)
	assert.Equal(t, 2, countOccurrences(got, config.RedactedPlaceholder))
}

// countOccurrences counts non-overlapping occurrences of needle in haystack.
func countOccurrences(haystack, needle string) int {
	count := 0
	for {
		index := indexOf(haystack, needle)
		if index < 0 {
			return count
		}
		count++
		haystack = haystack[index+len(needle):]
	}
}

// indexOf reports the first index of needle, or -1.
func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func Test_RedactMap_returns_a_new_map_and_does_not_mutate_the_input(t *testing.T) {
	// Given
	const secret = "sk-map-secret"
	t.Setenv("NINEROUTER_KEY", secret)
	cfg := config.Default()

	input := map[string]string{
		"auth":   "Bearer " + secret,
		"target": "example.com",
	}

	// When
	got := cfg.RedactMap(input)

	// Then
	require.NotSame(t, &input, &got)
	assert.Equal(t, "Bearer "+secret, input["auth"], "the input map must be untouched")
	assert.NotContains(t, got["auth"], secret)
	assert.Equal(t, "example.com", got["target"])
}

func Test_RedactMap_returns_nil_for_a_nil_map(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	got := cfg.RedactMap(nil)

	// Then
	assert.Nil(t, got)
}

func Test_SecretEnvNames_includes_provider_and_osint_variables(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	names := cfg.SecretEnvNames()

	// Then
	assert.Contains(t, names, "NINEROUTER_KEY")
	assert.Contains(t, names, "SHODAN_API_KEY")
	assert.Contains(t, names, "VT_API_KEY")
}

func Test_SecretEnvNames_deduplicates_repeated_env_names(t *testing.T) {
	// Given
	cfg := config.Default()
	cfg.OSINT.APIKeysEnv["duplicate"] = cfg.Providers["9router"].APIKeyEnv

	// When
	names := cfg.SecretEnvNames()

	// Then
	seen := make(map[string]int, len(names))
	for _, name := range names {
		seen[name]++
	}
	for name, count := range seen {
		assert.Equal(t, 1, count, "%s must appear once", name)
	}
}

func Test_SecretEnvNames_returns_a_deterministic_order_when_called_repeatedly(t *testing.T) {
	// Given
	cfg := config.Default()

	// When
	first := cfg.SecretEnvNames()
	second := cfg.SecretEnvNames()

	// Then
	assert.Equal(t, first, second)
}
