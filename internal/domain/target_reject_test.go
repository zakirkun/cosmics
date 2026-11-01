package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_NewTarget_RejectsValue_when_LengthExceedsLimit(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "one over limit", input: strings.Repeat("a", 513)},
		{name: "far over limit", input: strings.Repeat("a", 10_000)},
		{name: "over limit after trimming", input: "  " + strings.Repeat("a", 513) + "  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			target, err := domain.NewTarget(domain.TargetDomain, input)

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_RejectsInput_when_KindIsUndeclared(t *testing.T) {
	tests := []struct {
		name string
		kind domain.TargetKind
	}{
		{name: "empty", kind: domain.TargetKind("")},
		{name: "unknown", kind: domain.TargetKind("bogus")},
		{name: "wrong case", kind: domain.TargetKind("Domain")},
		{name: "padded", kind: domain.TargetKind(" domain ")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			kind := tt.kind

			// When
			target, err := domain.NewTarget(kind, "example.com")

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_RejectsValue_when_ValueIsBlank(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "single space", input: " "},
		{name: "spaces only", input: "     "},
		{name: "tabs only", input: "\t\t\t"},
		{name: "newlines only", input: "\r\n\r\n"},
		{name: "mixed whitespace only", input: " \t\r\n "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			target, err := domain.NewTarget(domain.TargetDomain, input)

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_RejectsValue_when_ValueContainsControlCharacter(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "nul", input: "exam\x00ple.com"},
		{name: "unit separator", input: "exam\x1fple.com"},
		{name: "delete", input: "exam\x7fple.com"},
		{name: "vertical tab", input: "exam\x0bple.com"},
		{name: "form feed", input: "exam\x0cple.com"},
		{name: "bell", input: "exam\x07ple.com"},
		{name: "escape", input: "exam\x1bple.com"},
		{name: "embedded control after space", input: " example.com\x00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			target, err := domain.NewTarget(domain.TargetDomain, input)

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_RejectsValue_when_ValueContainsShellMetacharacter(t *testing.T) {
	tests := []struct {
		name string
		char rune
	}{
		{name: "semicolon", char: ';'},
		{name: "pipe", char: '|'},
		{name: "ampersand", char: '&'},
		{name: "dollar", char: '$'},
		{name: "backtick", char: '`'},
		{name: "greater than", char: '>'},
		{name: "less than", char: '<'},
		{name: "line feed", char: '\n'},
		{name: "carriage return", char: '\r'},
		{name: "backslash", char: '\\'},
		{name: "double quote", char: '"'},
		{name: "single quote", char: '\''},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := "example.com" + string(tt.char) + "id"

			// When
			target, err := domain.NewTarget(domain.TargetDomain, input)

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget, "metacharacter %q must be rejected", tt.char)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_RejectsValue_when_MetacharacterIsTheWholeValue(t *testing.T) {
	// Given
	input := ";"

	// When
	target, err := domain.NewTarget(domain.TargetDomain, input)

	// Then
	require.ErrorIs(t, err, domain.ErrBadTarget)
	require.Equal(t, domain.Target{}, target)
}

func Test_NewTarget_ChecksKindBeforeValue_when_BothAreInvalid(t *testing.T) {
	tests := []struct {
		name  string
		kind  domain.TargetKind
		input string
	}{
		{name: "unknown kind and empty value", kind: domain.TargetKind("bogus"), input: ""},
		{name: "unknown kind and metacharacter", kind: domain.TargetKind("bogus"), input: "a;b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			kind, input := tt.kind, tt.input

			// When
			target, err := domain.NewTarget(kind, input)

			// Then
			assert.ErrorIs(t, err, domain.ErrBadTarget)
			assert.Equal(t, domain.Target{}, target)
		})
	}
}

func Test_NewTarget_AcceptsValue_when_MetacharacterLookalikesAreHarmless(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "parentheses", input: "example.com/(a)"},
		{name: "brackets", input: "example.com/[a]"},
		{name: "braces", input: "example.com/{a}"},
		{name: "equals", input: "example.com?a=b"},
		{name: "hash", input: "example.com/#frag"},
		{name: "plus", input: "example.com/a+b"},
		{name: "percent encoding", input: "example.com/%2F"},
		{name: "comma", input: "example.com,a"},
		{name: "colon", input: "example.com:8443"},
		{name: "at sign", input: "user@example.com"},
		{name: "tilde", input: "example.com/~user"},
		{name: "caret", input: "example.com/^a"},
		{name: "exclamation", input: "example.com/!a"},
		{name: "asterisk", input: "*.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			target, err := domain.NewTarget(domain.TargetURL, input)

			// Then
			assert.NoError(t, err)
			assert.Equal(t, input, target.Value)
			assert.Equal(t, input, target.Raw)
		})
	}
}

func Test_NewTarget_PreservesKind_when_ValueIsAccepted(t *testing.T) {
	// Given
	kinds := []domain.TargetKind{
		domain.TargetDomain, domain.TargetHost, domain.TargetIP,
		domain.TargetCIDR, domain.TargetURL, domain.TargetRepo, domain.TargetBinary,
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			// When
			target, err := domain.NewTarget(kind, "value")

			// Then
			require.NoError(t, err)
			require.Equal(t, kind, target.Kind)
			require.True(t, target.Kind.Valid())
			require.True(t, target.Value == target.Raw)
		})
	}
}

func Test_NewTarget_IsCasePreserving_when_ValueIsAccepted(t *testing.T) {
	// Given
	input := "Example.COM/Path"

	// When
	target, err := domain.NewTarget(domain.TargetURL, input)

	// Then
	require.NoError(t, err)
	require.Equal(t, input, target.Value)
	require.Equal(t, input, target.Raw)
}
