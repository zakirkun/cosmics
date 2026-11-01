package config

import (
	"os"
	"slices"
	"strings"
)

// RedactedPlaceholder replaces any secret value found in text.
const RedactedPlaceholder = "[REDACTED]"

// SecretEnvNames returns the ordered, de-duplicated names of every environment
// variable that may hold a secret.
func (c Config) SecretEnvNames() []string {
	var names []string
	seen := make(map[string]struct{})

	add := func(name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		names = append(names, trimmed)
	}

	for _, provider := range sortedProviders(c.Providers) {
		add(provider.APIKeyEnv)
	}

	for _, source := range sortedKeys(c.OSINT.APIKeysEnv) {
		add(c.OSINT.APIKeysEnv[source])
	}

	return names
}

// Redact replaces every present secret VALUE with the placeholder. A variable
// that is unset is skipped rather than treated as an empty secret, so Redact
// never replaces every character in a string.
func (c Config) Redact(text string) string {
	if text == "" {
		return text
	}

	out := text
	for _, name := range c.SecretEnvNames() {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		out = strings.ReplaceAll(out, value, RedactedPlaceholder)
	}
	return out
}

// RedactMap returns a new map with redacted values, leaving the input untouched.
func (c Config) RedactMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key] = c.Redact(value)
	}
	return out
}

// sortedProviders returns provider values ordered by their map key, so
// SecretEnvNames is deterministic.
func sortedProviders(providers map[string]Provider) []Provider {
	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]Provider, 0, len(keys))
	for _, key := range keys {
		out = append(out, providers[key])
	}
	return out
}

// sortedKeys returns a map's keys in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
