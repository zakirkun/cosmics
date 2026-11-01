package version_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cosmics/cosmics/internal/version"
)

func Test_String_includes_all_build_metadata_when_values_are_set(t *testing.T) {
	// Given
	original := struct{ v, c, d string }{version.Version, version.Commit, version.BuildDate}
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildDate = original.v, original.c, original.d
	})
	version.Version = "1.2.3"
	version.Commit = "abc1234"
	version.BuildDate = "2026-11-01"

	// When
	got := version.String()

	// Then
	assert.Contains(t, got, "cosmics")
	assert.Contains(t, got, "1.2.3")
	assert.Contains(t, got, "abc1234")
	assert.Contains(t, got, "2026-11-01")
}

func Test_String_is_a_single_line_when_called(t *testing.T) {
	// Given / When
	got := version.String()

	// Then
	assert.NotContains(t, got, "\n")
	assert.False(t, strings.HasPrefix(got, " "))
}
