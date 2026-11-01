package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

// mustRegistry loads the embedded registry or fails the test.
func mustRegistry(t *testing.T) *Registry {
	t.Helper()

	registry, err := Default()
	require.NoError(t, err)
	return registry
}

func Test_Default_loads_every_shipped_tool_when_called(t *testing.T) {
	// Given / When
	registry := mustRegistry(t)

	// Then
	assert.GreaterOrEqual(t, registry.Len(), 5, "the shipped registry must define the core tools")

	for _, id := range []string{"nmap", "httpx", "subfinder", "nuclei", "ffuf"} {
		tool, err := registry.Get(id)
		require.NoError(t, err, "tool %q must be present", id)
		assert.NoError(t, tool.Validate(), "tool %q must validate", id)
		assert.Equal(t, "cosmics/kali-tools:latest", tool.Image)
	}
}

func Test_All_is_sorted_by_id_when_called_twice(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	first := registry.All()
	second := registry.All()

	// Then
	require.NotEmpty(t, first)
	for i := 1; i < len(first); i++ {
		assert.Less(t, first[i-1].ID, first[i].ID, "tools must be sorted for deterministic output")
	}
	assert.Equal(t, first, second)
}

func Test_Get_returns_ErrToolNotFound_when_the_id_is_absent(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	_, err := registry.Get("no_such_tool")

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrToolNotFound)
}

func Test_ForStageAllowList_includes_recon_network_and_web_when_the_stage_is_recon(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	tools := registry.ForStageAllowList(domain.StageRecon, domain.ModeFull)

	// Then
	require.NotEmpty(t, tools)

	for _, tool := range tools {
		assert.Contains(t,
			[]Category{CategoryRecon, CategoryNetwork, CategoryWeb},
			tool.Category,
			"tool %q has category %q which recon must not offer", tool.ID, tool.Category)
	}
}

func Test_ForStageAllowList_returns_nothing_when_the_stage_is_analysis(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	tools := registry.ForStageAllowList(domain.StageAnalysis, domain.ModeFull)

	// Then
	assert.Empty(t, tools, "analysis reasons over stored facts; it runs no external tool")
}

func Test_ForStageAllowList_includes_only_subdomain_tools_when_the_stage_is_osint(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	tools := registry.ForStageAllowList(domain.StageOSINT, domain.ModeFull)

	// Then
	require.NotEmpty(t, tools)
	for _, tool := range tools {
		assert.Equal(t, CategoryOSINT, tool.Category)
	}
}

func Test_Authorize_rejects_an_exploit_tool_when_the_mode_is_recon(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	err := registry.Authorize("nmap", domain.StageRecon, domain.ModeRecon)

	// Then
	assert.NoError(t, err, "nmap is a recon tool and is allowed")
}

func Test_Authorize_rejects_a_tool_from_the_wrong_stage_when_called(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	err := registry.Authorize("ffuf", domain.StageOSINT, domain.ModeFull)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStageForbidden)
}

func Test_Authorize_rejects_an_unknown_tool_when_called(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	err := registry.Authorize("ghost_tool", domain.StageRecon, domain.ModeFull)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrToolNotFound)
}

func Test_BuildInvocation_accepts_valid_arguments_when_every_constraint_is_met(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"target":"example.com","service_detection":true,"top_ports":100}`)

	// When
	invocation, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "nmap", invocation.Tool.ID)
	assert.Equal(t, "example.com", invocation.Target)
}

func Test_BuildInvocation_rejects_a_missing_required_field_when_omitted(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"service_detection":true}`)

	// When
	_, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
	assert.Contains(t, err.Error(), "target")
}

func Test_BuildInvocation_rejects_an_unknown_field_when_additional_properties_is_false(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"target":"example.com","--script":"http-shellshock"}`)

	// When
	_, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
	assert.Contains(t, err.Error(), "--script")
}

func Test_BuildInvocation_rejects_a_wrong_type_when_a_string_is_given_for_an_integer(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"target":"example.com","top_ports":"100"}`)

	// When
	_, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
}

func Test_BuildInvocation_rejects_an_out_of_range_value_when_minimum_is_violated(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"target":"example.com","top_ports":0}`)

	// When
	_, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
}

func Test_BuildInvocation_rejects_a_pattern_miss_when_the_target_contains_metacharacters(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{"semicolon", "example.com;id"},
		{"pipe", "example.com|id"},
		{"backtick", "example.com`id`"},
		{"dollar", "example.com$(id)"},
		{"space", "example.com example.org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			registry := mustRegistry(t)
			args, marshalErr := json.Marshal(map[string]any{"target": tt.target})
			require.NoError(t, marshalErr)

			// When
			_, err := BuildInvocation(registry, "nmap", args)

			// Then
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidArgs)
		})
	}
}

func Test_BuildInvocation_rejects_an_enum_miss_when_the_value_is_outside_the_set(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`{"target":"example.com","severity":"catastrophic"}`)

	// When
	_, err := BuildInvocation(registry, "nuclei", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
	assert.Contains(t, err.Error(), "severity")
}

func Test_BuildInvocation_rejects_arguments_that_are_not_an_object_when_given_an_array(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	args := json.RawMessage(`["example.com"]`)

	// When
	_, err := BuildInvocation(registry, "nmap", args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgs)
}
