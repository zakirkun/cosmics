// Package osint collects public intelligence about a target through direct
// calls to well-known public APIs.
//
// These adapters deliberately do not run inside a container and do not touch the
// target host: they query third-party indexes over plain HTTPS, which is exactly
// the traffic an engagement's scope authorises. A source that needs a paid key
// is simply not registered, so the package degrades to the free set.
package osint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors surfaced by the package.
var (
	// ErrUnknownSource is returned when a registry lookup misses.
	ErrUnknownSource = errors.New("osint: unknown source")

	// ErrBadResponse is returned when a source answers with a non-2xx status,
	// an oversized body, or a payload that does not match its documented shape.
	ErrBadResponse = errors.New("osint: malformed source response")

	// ErrInvalidTarget is returned when a target is not a hostname-shaped value.
	// It is the boundary that keeps hostile data away from every network call.
	ErrInvalidTarget = errors.New("osint: invalid target")

	// ErrNoClient is returned when an adapter was built without an HTTP client.
	ErrNoClient = errors.New("osint: http client is nil")
)

const (
	// maxResponseBytes bounds one source response. OSINT feeds are public and
	// occasionally hostile; an unbounded read is a memory-exhaustion path.
	maxResponseBytes = 8 << 20

	// defaultTimeout bounds one source query end to end.
	defaultTimeout = 20 * time.Second

	// defaultMaxRecords bounds one query's output so a pathological feed cannot
	// turn into an unbounded asset insert loop.
	defaultMaxRecords = 5000

	// maxTargetLength is the longest DNS name the DNS wire format allows.
	maxTargetLength = 253
)

// Record is one observation a source made about a target.
type Record struct {
	Kind       string
	Value      string
	Source     string
	Confidence float64
	Attrs      map[string]string
}

// Source is one public-data provider.
type Source interface {
	// Name is the stable identifier used in configuration and asset provenance.
	Name() string

	// Query returns observations about target. It must honour ctx.
	Query(ctx context.Context, target string) ([]Record, error)
}

// HTTPDoer is the narrow HTTP surface every HTTP adapter needs, so a test can
// inject an httptest server's client and no adapter has to reach for a global.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Settings holds the tunables shared by the HTTP adapters.
type Settings struct {
	// BaseURL replaces the production endpoint. Tests point it at httptest.
	BaseURL string

	// Timeout bounds one query end to end.
	Timeout time.Duration

	// MaxRecords bounds one query's output so a pathological feed cannot turn
	// into an unbounded asset insert loop.
	MaxRecords int
}

// Option customises an HTTP adapter at construction.
type Option func(*Settings)

// WithBaseURL overrides the adapter's endpoint, which is how a test redirects
// a query at an httptest server instead of the public API.
func WithBaseURL(baseURL string) Option {
	return func(s *Settings) { s.BaseURL = strings.TrimSuffix(baseURL, "/") }
}

// WithTimeout overrides the per-query deadline.
func WithTimeout(timeout time.Duration) Option {
	return func(s *Settings) { s.Timeout = timeout }
}

// WithMaxRecords overrides the per-query record cap.
func WithMaxRecords(limit int) Option {
	return func(s *Settings) { s.MaxRecords = limit }
}

// newSettings applies options over defaults, keeping the required base URL.
func newSettings(defaultBaseURL string, opts []Option) Settings {
	settings := Settings{BaseURL: defaultBaseURL, Timeout: defaultTimeout, MaxRecords: defaultMaxRecords}
	for _, opt := range opts {
		if opt != nil {
			opt(&settings)
		}
	}
	if settings.Timeout <= 0 {
		settings.Timeout = defaultTimeout
	}
	if settings.MaxRecords <= 0 {
		settings.MaxRecords = defaultMaxRecords
	}
	return settings
}

// Registry holds a set of sources addressable by name.
type Registry struct {
	sources []Source
	byName  map[string]Source
}

// NewRegistry builds a registry. Duplicate names are collapsed to the last
// registration so a caller assembling the default set cannot make Get ambiguous.
func NewRegistry(sources ...Source) *Registry {
	registry := &Registry{
		sources: make([]Source, 0, len(sources)),
		byName:  make(map[string]Source, len(sources)),
	}
	for _, source := range sources {
		if source == nil {
			continue
		}
		name := source.Name()
		if _, exists := registry.byName[name]; exists {
			for i, existing := range registry.sources {
				if existing.Name() == name {
					registry.sources[i] = source
					break
				}
			}
			registry.byName[name] = source
			continue
		}
		registry.sources = append(registry.sources, source)
		registry.byName[name] = source
	}
	return registry
}

// All returns every registered source sorted by name.
//
// Sorting, not registration order, is the contract: a caller that iterates All
// must produce the same sequence on every run regardless of how the set was
// assembled.
func (r *Registry) All() []Source {
	if r == nil {
		return nil
	}
	out := make([]Source, len(r.sources))
	copy(out, r.sources)
	sortSources(out)
	return out
}

// Names returns every registered source name in the same order as All.
func (r *Registry) Names() []string {
	sources := r.All()
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name())
	}
	return names
}

// Get returns the source with the given name.
func (r *Registry) Get(name string) (Source, error) {
	trimmed := strings.TrimSpace(name)
	if r == nil {
		return nil, fmt.Errorf("source %q: %w", trimmed, ErrUnknownSource)
	}
	source, ok := r.byName[trimmed]
	if !ok {
		return nil, fmt.Errorf("source %q (registered: %s): %w",
			trimmed, strings.Join(r.Names(), ", "), ErrUnknownSource)
	}
	return source, nil
}

// Enabled returns a new registry holding only the named sources.
//
// Unknown names are skipped rather than fatal: configuration may legitimately
// list an adapter this build does not ship, and the stage records that gap
// instead of refusing to run OSINT at all. The caller's order is preserved;
// the stage iterates All, which sorts.
func (r *Registry) Enabled(names []string) *Registry {
	if r == nil {
		return NewRegistry()
	}

	seen := make(map[string]bool, len(names))
	enabled := make([]Source, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		if source, ok := r.byName[trimmed]; ok {
			enabled = append(enabled, source)
		}
	}
	return NewRegistry(enabled...)
}

// sortSources orders sources by name using insertion sort, which is adequate
// for a dozen adapters and avoids an allocation for the common case.
func sortSources(sources []Source) {
	for i := 1; i < len(sources); i++ {
		for j := i; j > 0 && sources[j-1].Name() > sources[j].Name(); j-- {
			sources[j-1], sources[j] = sources[j], sources[j-1]
		}
	}
}

// ValidateTarget rejects a target that is not a plain hostname.
//
// Every adapter calls this before building a URL or asking a resolver: a value
// carrying shell metacharacters, whitespace, or control characters is a sign the
// data is hostile, and it must never leave this package as a query.
func ValidateTarget(target string) error {
	if target == "" {
		return fmt.Errorf("target is empty: %w", ErrInvalidTarget)
	}
	if len(target) > maxTargetLength {
		return fmt.Errorf("target %q exceeds %d characters: %w", target, maxTargetLength, ErrInvalidTarget)
	}
	if target != strings.TrimSpace(target) {
		return fmt.Errorf("target %q has surrounding whitespace: %w", target, ErrInvalidTarget)
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("target %q contains control character %q: %w", target, r, ErrInvalidTarget)
		}
		if isShellMetachar(r) {
			return fmt.Errorf("target %q contains shell metacharacter %q: %w", target, r, ErrInvalidTarget)
		}
		if r == ' ' || r == '\t' || r == '/' || r == '\\' {
			return fmt.Errorf("target %q is not a bare hostname: %w", target, ErrInvalidTarget)
		}
	}
	return nil
}

// isShellMetachar mirrors the domain target rule for value validation.
func isShellMetachar(r rune) bool {
	switch r {
	case ';', '|', '&', '$', '`', '>', '<', '\n', '\r', '"', '\'':
		return true
	}
	return false
}

// fetch performs one bounded GET and returns the response body.
func fetch(ctx context.Context, client HTTPDoer, rawURL string, timeout time.Duration) ([]byte, error) {
	if client == nil {
		return nil, ErrNoClient
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "cosmics-osint/1 (authorized assessment)")

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", rawURL, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s returned %s: %w", rawURL, response.Status, ErrBadResponse)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", rawURL, err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes: %w", rawURL, maxResponseBytes, ErrBadResponse)
	}
	return body, nil
}
