package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_TargetKind_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name string
		kind domain.TargetKind
		want bool
	}{
		{name: "domain", kind: domain.TargetDomain, want: true},
		{name: "host", kind: domain.TargetHost, want: true},
		{name: "ip", kind: domain.TargetIP, want: true},
		{name: "cidr", kind: domain.TargetCIDR, want: true},
		{name: "url", kind: domain.TargetURL, want: true},
		{name: "repo", kind: domain.TargetRepo, want: true},
		{name: "binary", kind: domain.TargetBinary, want: true},
		{name: "empty", kind: domain.TargetKind(""), want: false},
		{name: "unknown", kind: domain.TargetKind("bogus"), want: false},
		{name: "wrong case", kind: domain.TargetKind("Domain"), want: false},
		{name: "padded", kind: domain.TargetKind(" domain "), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			kind := tt.kind

			// When
			got := kind.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_NewTarget_AcceptsValue_when_KindAndValueAreValid(t *testing.T) {
	tests := []struct {
		name  string
		kind  domain.TargetKind
		input string
		want  string
	}{
		{name: "domain", kind: domain.TargetDomain, input: "example.com", want: "example.com"},
		{name: "host", kind: domain.TargetHost, input: "app.internal", want: "app.internal"},
		{name: "ipv4", kind: domain.TargetIP, input: "10.0.0.5", want: "10.0.0.5"},
		{name: "ipv6", kind: domain.TargetIP, input: "::1", want: "::1"},
		{name: "cidr", kind: domain.TargetCIDR, input: "10.0.0.0/24", want: "10.0.0.0/24"},
		{name: "url", kind: domain.TargetURL, input: "https://example.com/login?next=/a", want: "https://example.com/login?next=/a"},
		{name: "repo", kind: domain.TargetRepo, input: "https://github.com/acme/app.git", want: "https://github.com/acme/app.git"},
		{name: "binary", kind: domain.TargetBinary, input: "app.exe", want: "app.exe"},
		{name: "underscores and hyphens", kind: domain.TargetHost, input: "my_host-01.corp", want: "my_host-01.corp"},
		{name: "unicode label", kind: domain.TargetDomain, input: "exämple.com", want: "exämple.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			kind, input := tt.kind, tt.input

			// When
			target, err := domain.NewTarget(kind, input)

			// Then
			assert.NoError(t, err)
			assert.Equal(t, kind, target.Kind)
			assert.Equal(t, tt.want, target.Value)
			assert.Equal(t, input, target.Raw)
			assert.True(t, target.ID.IsZero())
			assert.True(t, target.EngagementID.IsZero())
			assert.Empty(t, target.Note)
			assert.True(t, target.CreatedAt.IsZero())
		})
	}
}

func Test_NewTarget_TrimsValueButKeepsRaw_when_InputIsPadded(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "leading and trailing spaces", input: "   example.com   ", want: "example.com"},
		{name: "tabs", input: "\texample.com\t", want: "example.com"},
		{name: "windows newlines", input: "\r\nexample.com\r\n", want: "example.com"},
		{name: "mixed whitespace", input: " \t\nexample.com \r\n", want: "example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			target, err := domain.NewTarget(domain.TargetDomain, input)

			// Then
			require.NoError(t, err)
			assert.Equal(t, tt.want, target.Value)
			assert.Equal(t, input, target.Raw, "Raw must preserve the operator input exactly")
		})
	}
}

func Test_NewTarget_AcceptsValue_when_LengthIsExactlyAtLimit(t *testing.T) {
	// Given
	input := strings.Repeat("a", 512)

	// When
	target, err := domain.NewTarget(domain.TargetDomain, input)

	// Then
	require.NoError(t, err)
	require.Len(t, target.Value, 512)
	require.Equal(t, input, target.Raw)
}
