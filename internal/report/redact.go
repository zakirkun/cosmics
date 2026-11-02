package report

import (
	"sort"
	"strings"
)

// RedactedPlaceholder is what every secret becomes.
const RedactedPlaceholder = "[REDACTED]"

// Redact replaces every secret in text, longest first.
//
// Longest-first matters: given secrets "abc" and "abcdef", replacing the short one
// first would leave "def" behind from the longer value. Sorting by descending
// length means the longer secret is consumed before the shorter one can split it.
//
// Empty secrets are skipped: replacing "" would insert the placeholder between
// every character.
func Redact(text string, secrets []string) string {
	ordered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			ordered = append(ordered, secret)
		}
	}

	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })

	result := text
	for _, secret := range ordered {
		result = strings.ReplaceAll(result, secret, RedactedPlaceholder)
	}
	return result
}
