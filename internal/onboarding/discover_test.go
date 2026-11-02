package onboarding_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/onboarding"
)

// fakeDiscoverer returns a fixed catalogue and records the endpoint and key it
// was asked about.
type fakeDiscoverer struct {
	models   []string
	err      error
	calls    int
	endpoint string
	apiKey   string
}

func (f *fakeDiscoverer) Models(_ context.Context, endpoint, apiKey string) ([]string, error) {
	f.calls++
	f.endpoint = endpoint
	f.apiKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func Test_Interactive_uses_the_discovered_model_when_selected_by_number(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection", "cutad/deepseek-v4.1-flash"}}
	input := "\n\n2\n\ny\n"

	// When
	var out bytes.Buffer
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), &out, discoverer)

	// Then
	require.NoError(t, err)
	chain := cfg.ModelProfiles[onboarding.DefaultProfileName][domain.StageRecon]
	require.NotEmpty(t, chain)
	assert.Equal(t, "cutad/deepseek-v4.1-flash", chain[0].Model,
		"menu entry 2 is the second discovered model")
	assert.Contains(t, out.String(), "astral-projection", "the list must show what was discovered")
}

func Test_Interactive_filters_the_list_when_the_operator_types_a_search_term(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection", "cutad/deepseek-v4.1-flash"}}
	input := "\n\ndeepseek\n1\n\ny\n"

	// When
	var out bytes.Buffer
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), &out, discoverer)

	// Then
	require.NoError(t, err)
	chain := cfg.ModelProfiles[onboarding.DefaultProfileName][domain.StageRecon]
	require.NotEmpty(t, chain)
	assert.Equal(t, "cutad/deepseek-v4.1-flash", chain[0].Model,
		"after filtering, entry 1 is the only remaining match")
	assert.Contains(t, out.String(), "> 1. cutad/deepseek-v4.1-flash\n",
		"the filtered list must renumber the single remaining match")
	assert.Equal(t, 2, strings.Count(out.String(), "Number, search term"),
		"one list render per read: the filter term, then the selection")
}

func Test_Interactive_accepts_a_manually_typed_model_when_discovery_fails(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{err: errors.New("connection refused")}
	input := "\n\nmy-local-model\n\ny\n"

	// When
	var out bytes.Buffer
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), &out, discoverer)

	// Then
	require.NoError(t, err)
	chain := cfg.ModelProfiles[onboarding.DefaultProfileName][domain.StageRecon]
	require.NotEmpty(t, chain)
	assert.Equal(t, "my-local-model", chain[0].Model)
	assert.Contains(t, out.String(), "connection refused", "the operator must see why discovery failed")
}

func Test_Interactive_returns_ErrAborted_when_input_ends_early(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection"}}

	// When
	var out bytes.Buffer
	_, err := onboarding.Interactive(context.Background(), strings.NewReader(""), &out, discoverer)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, onboarding.ErrAborted)
}

func Test_Interactive_does_not_echo_the_api_key_in_its_output(t *testing.T) {
	// Given
	const key = "sk-super-secret-token"
	discoverer := &fakeDiscoverer{models: []string{"astral-projection"}}
	input := "\n" + key + "\n1\n\ny\n"

	// When
	var out bytes.Buffer
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), &out, discoverer)

	// Then
	require.NoError(t, err)
	assert.NotContains(t, out.String(), key, "SECURITY: the key must never be echoed to the terminal")
	assert.Equal(t, key, discoverer.apiKey, "the key must reach the discoverer")
	assert.Equal(t, "NINEROUTER_KEY", cfg.Providers["9router"].APIKeyEnv,
		"only the variable name is persisted")
}

func Test_Interactive_reads_the_endpoint_override(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection"}}
	input := "http://gateway.internal:20128/v1\n\n1\n\ny\n"

	// When
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), new(bytes.Buffer), discoverer)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "http://gateway.internal:20128/v1", cfg.Providers["9router"].BaseURL)
	assert.Equal(t, "http://gateway.internal:20128/v1", discoverer.endpoint,
		"discovery must use the endpoint the operator just typed")
}

func Test_Interactive_retries_discovery_when_the_operator_presses_enter(t *testing.T) {
	// Given
	discoverer := &flakyDiscoverer{models: []string{"astral-projection"}}
	input := "\n\n\n1\n\ny\n"

	// When
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), new(bytes.Buffer), discoverer)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, discoverer.calls, "one failure followed by one retry")
	chain := cfg.ModelProfiles[onboarding.DefaultProfileName][domain.StageRecon]
	require.NotEmpty(t, chain)
	assert.Equal(t, "astral-projection", chain[0].Model)
}

func Test_Interactive_rejects_a_declined_confirmation(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection"}}
	input := "\n\n1\nn\n"

	// When
	_, err := onboarding.Interactive(context.Background(), strings.NewReader(input), new(bytes.Buffer), discoverer)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, onboarding.ErrAborted)
}

func Test_Interactive_skips_discovery_when_no_discoverer_is_supplied(t *testing.T) {
	// Given
	input := "\n\nmanual-model\n\ny\n"

	// When
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), new(bytes.Buffer), nil)

	// Then
	require.NoError(t, err)
	chain := cfg.ModelProfiles[onboarding.DefaultProfileName][domain.StageRecon]
	require.NotEmpty(t, chain)
	assert.Equal(t, "manual-model", chain[0].Model)
}

func Test_Interactive_returns_a_config_that_passes_validation(t *testing.T) {
	// Given
	discoverer := &fakeDiscoverer{models: []string{"astral-projection"}}
	input := "\n\n1\n\ny\n"

	// When
	cfg, err := onboarding.Interactive(context.Background(), strings.NewReader(input), new(bytes.Buffer), discoverer)

	// Then
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, onboarding.DefaultAPIKeyEnv, cfg.Providers["9router"].APIKeyEnv)
}

// flakyDiscoverer fails its first call and succeeds afterwards.
type flakyDiscoverer struct {
	models []string
	calls  int
}

func (f *flakyDiscoverer) Models(context.Context, string, string) ([]string, error) {
	f.calls++
	if f.calls == 1 {
		return nil, errors.New("temporary failure")
	}
	return f.models, nil
}
