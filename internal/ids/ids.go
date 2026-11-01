package ids

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// ErrUnknownID is returned when an identifier is not present in the reference set.
var ErrUnknownID = errors.New("ids: unknown reference identifier")

//go:embed wstg.json
var wstgJSON []byte

//go:embed attack.json
var attackJSON []byte

// Reference is one entry in a validated identifier set.
type Reference struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Set is an immutable, validated collection of reference identifiers.
type Set struct {
	name    string
	members map[string]Reference
	pattern *regexp.Regexp
}

var (
	loadOnce sync.Once
	wstg     *Set
	attack   *Set
	loadErr  error
)

// load parses both embedded sets exactly once.
func load() (*Set, *Set, error) {
	loadOnce.Do(func() {
		wstg, loadErr = parseSet("wstg", wstgJSON, regexp.MustCompile(`^WSTG-[A-Z]{4}-\d{2}$`))
		if loadErr != nil {
			return
		}
		attack, loadErr = parseSet("attack", attackJSON, regexp.MustCompile(`^T\d{4}(?:\.\d{3})?$`))
	})
	return wstg, attack, loadErr
}

// parseSet decodes and validates one embedded reference table.
func parseSet(name string, raw []byte, pattern *regexp.Regexp) (*Set, error) {
	var refs []Reference
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, fmt.Errorf("decode %s references: %w", name, err)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("decode %s references: empty set", name)
	}

	members := make(map[string]Reference, len(refs))
	for _, ref := range refs {
		if !pattern.MatchString(ref.ID) {
			return nil, fmt.Errorf("%s reference %q does not match %s", name, ref.ID, pattern)
		}
		if ref.Title == "" {
			return nil, fmt.Errorf("%s reference %q has no title", name, ref.ID)
		}
		if _, exists := members[ref.ID]; exists {
			return nil, fmt.Errorf("%s reference %q is duplicated", name, ref.ID)
		}
		members[ref.ID] = ref
	}

	return &Set{name: name, members: members, pattern: pattern}, nil
}

// WSTG returns the OWASP Web Security Testing Guide identifier set.
func WSTG() (*Set, error) {
	w, _, err := load()
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ATTACK returns the MITRE ATT&CK technique identifier set.
func ATTACK() (*Set, error) {
	_, a, err := load()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Has reports whether id is a member of the set.
func (s *Set) Has(id string) bool {
	_, ok := s.members[id]
	return ok
}

// Lookup returns the reference for id.
func (s *Set) Lookup(id string) (Reference, error) {
	ref, ok := s.members[id]
	if !ok {
		return Reference{}, fmt.Errorf("%s %q: %w", s.name, id, ErrUnknownID)
	}
	return ref, nil
}

// Normalize returns the canonical identifier, or an error when it is unknown.
// An empty input returns an empty string and no error, so a caller can blank an
// invalid identifier the model proposed without failing the whole finding.
func (s *Set) Normalize(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if !s.Has(id) {
		return "", fmt.Errorf("%s %q: %w", s.name, id, ErrUnknownID)
	}
	return id, nil
}

// Len returns the number of reference identifiers.
func (s *Set) Len() int { return len(s.members) }

// Name returns the set's short name.
func (s *Set) Name() string { return s.name }
