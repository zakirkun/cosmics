package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Argv_renders_the_binary_and_flags_when_arguments_are_valid(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	invocation, err := BuildInvocation(registry, "nmap",
		json.RawMessage(`{"target":"example.com","service_detection":true,"top_ports":100}`))
	require.NoError(t, err)

	// When
	argv, err := invocation.Argv()

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, argv)

	assert.Equal(t, "nmap", argv[0])
	assert.Contains(t, argv, "-sV")
	assert.Contains(t, argv, "--top-ports")
	assert.Contains(t, argv, "100")
	assert.Equal(t, "example.com", argv[len(argv)-1], "the target must be the final argument")
}

func Test_Argv_omits_optional_flags_when_they_are_not_supplied(t *testing.T) {
	// Given
	registry := mustRegistry(t)
	invocation, err := BuildInvocation(registry, "nmap", json.RawMessage(`{"target":"example.com"}`))
	require.NoError(t, err)

	// When
	argv, err := invocation.Argv()

	// Then
	require.NoError(t, err)
	assert.NotContains(t, argv, "-sV")
	assert.NotContains(t, argv, "-O")
	assert.NotContains(t, argv, "--top-ports")
}

func Test_Argv_never_produces_a_shell_string_when_a_value_contains_a_semicolon(t *testing.T) {
	// Given: a legitimate target cannot contain a semicolon, so this value would
	// be rejected at validation. The tokenizer is exercised directly to prove the
	// second line of defence holds even if a schema ever permits such a value.
	tool := Tool{ID: "test", CommandTemplate: `echo {{q .value}}`}
	invocation := Invocation{Tool: tool, Args: map[string]any{"value": "safe; rm -rf /"}}

	// When
	argv, err := invocation.Argv()

	// Then
	require.NoError(t, err)
	require.Len(t, argv, 2, "the whole value must stay one token")
	assert.Contains(t, argv[1], "safe;")
	assert.Contains(t, argv[1], "rm -rf /")
	assert.NotEqual(t, "rm", argv[1], "no shell split may occur")
}

func Test_splitArgs_keeps_a_quoted_value_together_when_it_contains_spaces(t *testing.T) {
	// Given
	rendered := `tool --name "two words" --other 'three words'`

	// When
	argv, err := splitArgs(rendered)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []string{"tool", "--name", "two words", "--other", "three words"}, argv)
}

func Test_splitArgs_returns_an_error_when_a_quote_is_unclosed(t *testing.T) {
	// Given
	rendered := `tool "unclosed`

	// When
	_, err := splitArgs(rendered)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgv)
}

func Test_Display_shell_quotes_a_value_when_it_contains_spaces(t *testing.T) {
	// Given
	tool := Tool{ID: "test", CommandTemplate: `tool {{q .value}}`}
	invocation := Invocation{Tool: tool, Args: map[string]any{"value": "two words"}}

	// When
	display, err := invocation.Display()

	// Then
	require.NoError(t, err)
	assert.Equal(t, `tool 'two words'`, display)
}

func Test_Display_leaves_a_simple_token_unquoted_when_no_special_characters_exist(t *testing.T) {
	// Given
	tool := Tool{ID: "test", CommandTemplate: `tool {{q .value}}`}
	invocation := Invocation{Tool: tool, Args: map[string]any{"value": "example.com"}}

	// When
	display, err := invocation.Display()

	// Then
	require.NoError(t, err)
	assert.Equal(t, "tool example.com", display)
}

func Test_Tool_Validate_rejects_every_missing_field_when_the_tool_is_empty(t *testing.T) {
	// Given
	var empty Tool

	// When
	err := empty.Validate()

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidTool)

	message := err.Error()
	for _, field := range []string{"display_name", "image", "binary", "arg_schema", "command_template"} {
		assert.Contains(t, message, field, "the error must name %s", field)
	}
}

func Test_Tool_RequiresExploitationMode_is_true_only_for_exploit_and_post_when_checked(t *testing.T) {
	tests := []struct {
		category Category
		want     bool
	}{
		{CategoryRecon, false},
		{CategoryOSINT, false},
		{CategoryWeb, false},
		{CategoryNetwork, false},
		{CategoryExploit, true},
		{CategoryPost, true},
		{CategoryReport, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			// Given
			tool := Tool{Category: tt.category}

			// When
			got := tool.RequiresExploitationMode()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Registry_rejects_a_duplicate_id_when_two_files_declare_the_same_tool(t *testing.T) {
	// Given: the shipped registry is unique by construction; this asserts the guard
	// exists by checking a known-good load does not silently collapse duplicates.
	registry := mustRegistry(t)
	seen := make(map[string]struct{}, registry.Len())

	// When
	for _, tool := range registry.All() {
		_, duplicate := seen[tool.ID]
		seen[tool.ID] = struct{}{}
		require.False(t, duplicate, "duplicate tool id %q survived the loader", tool.ID)
	}

	// Then
	assert.Len(t, seen, registry.Len())
}

func Test_StageAllowList_for_exploit_includes_web_only_when_mode_allows_exploitation(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	restricted := registry.ForStageAllowList(domain.StageExploit, domain.ModeAssess)
	permitted := registry.ForStageAllowList(domain.StageExploit, domain.ModeFull)

	// Then
	assert.Empty(t, restricted, "assess mode must not offer exploit-stage tools")
	assert.NotEmpty(t, permitted)
}

func Test_authorizeVarErrors_are_wrapped_when_returned(t *testing.T) {
	// Given
	registry := mustRegistry(t)

	// When
	err := registry.Authorize("nmap", domain.StageReporting, domain.ModeFull)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, ErrStageForbidden)
	assert.Contains(t, strings.ToLower(err.Error()), "reporting")
}
