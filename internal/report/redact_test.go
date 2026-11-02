package report_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cosmics/cosmics/internal/report"
)

func Test_Redact_replaces_a_secret_when_it_appears(t *testing.T) {
	// Given
	text := "the token is sk-live-abc123 and nothing else"

	// When
	got := report.Redact(text, []string{"sk-live-abc123"})

	// Then
	assert.NotContains(t, got, "sk-live-abc123")
	assert.Contains(t, got, report.RedactedPlaceholder)
}

func Test_Redact_replaces_the_longest_secret_first_when_secrets_overlap(t *testing.T) {
	// Given: "abcdef" contains "abc". Replacing the short one first would leave
	// "def" behind, so the longer value must be consumed first.
	text := "value: abcdef"

	// When
	got := report.Redact(text, []string{"abc", "abcdef"})

	// Then
	assert.Equal(t, "value: "+report.RedactedPlaceholder, got)
	assert.NotContains(t, got, "def")
}

func Test_Redact_skips_an_empty_secret_when_given_one(t *testing.T) {
	// Given
	text := "plain"

	// When
	got := report.Redact(text, []string{""})

	// Then
	assert.Equal(t, "plain", got,
		"an empty secret must not be replaced everywhere")
}

func Test_Redact_returns_the_input_unchanged_when_no_secret_matches(t *testing.T) {
	// Given
	text := "no secrets here"

	// When
	got := report.Redact(text, []string{"absent"})

	// Then
	assert.Equal(t, text, got)
}

func Test_Redact_replaces_every_occurrence_when_a_secret_repeats(t *testing.T) {
	// Given
	text := "a hunter2 b hunter2 c hunter2"

	// When
	got := report.Redact(text, []string{"hunter2"})

	// Then
	assert.NotContains(t, got, "hunter2")
	assert.Equal(t, 3, countOccurrences(got, report.RedactedPlaceholder))
}

// countOccurrences counts non-overlapping occurrences of needle in text.
func countOccurrences(text, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			count++
			i += len(needle) - 1
		}
	}
	return count
}
