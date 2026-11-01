// Package domain holds pure types and invariants. It imports nothing but the
// standard library and performs no I/O.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors shared across the domain. Callers branch with errors.Is.
var (
	ErrEmptyID       = errors.New("domain: identifier is empty")
	ErrInvalidID     = errors.New("domain: identifier is not a valid ULID")
	ErrEmptyName     = errors.New("domain: name is empty")
	ErrNameTooLong   = errors.New("domain: name exceeds 200 characters")
	ErrUnknownMode   = errors.New("domain: unknown engagement mode")
	ErrUnknownStage  = errors.New("domain: unknown stage")
	ErrUnknownStatus = errors.New("domain: unknown engagement status")
	ErrIllegalMove   = errors.New("domain: illegal status transition")
	ErrNoTargets     = errors.New("domain: engagement requires at least one target")
	ErrUnauthorized  = errors.New("domain: authorization not recorded")
	ErrBadTarget     = errors.New("domain: invalid target value")
)

// ID is a ULID primary key. It is always 26 characters of Crockford base32 and
// sorts lexicographically in creation order.
type ID struct {
	raw string
}

// NewID parses and validates an existing ULID string.
func NewID(s string) (ID, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ID{}, ErrEmptyID
	}
	if _, err := ulidParse(trimmed); err != nil {
		return ID{}, fmt.Errorf("id %q: %w", trimmed, ErrInvalidID)
	}
	return ID{raw: strings.ToUpper(trimmed)}, nil
}

// IDGenerator produces new identifiers. Injecting it keeps time out of the
// domain and makes ID determinism testable.
type IDGenerator interface {
	New() ID
}

// String renders the raw ULID.
func (i ID) String() string { return i.raw }

// IsZero reports whether the identifier was never set.
func (i ID) IsZero() bool { return i.raw == "" }

// Equal compares two identifiers by value.
func (i ID) Equal(other ID) bool { return i.raw == other.raw }

// TimestampMs is a UTC instant in Unix milliseconds. All persisted times use
// this type so a seconds/milliseconds mixup is a compile error.
type TimestampMs int64

// Time converts to a time.Time in UTC.
func (t TimestampMs) Time() time.Time { return time.UnixMilli(int64(t)).UTC() }

// IsZero reports whether the timestamp was never set.
func (t TimestampMs) IsZero() bool { return t == 0 }

// Clock supplies the current instant. Domain code never calls time.Now.
type Clock interface {
	Now() TimestampMs
}

// Mode selects how far an engagement may proceed.
type Mode string

// Engagement modes, ordered by increasing authority.
const (
	ModeRecon   Mode = "recon"
	ModeAssess  Mode = "assess"
	ModeExploit Mode = "exploit"
	ModeFull    Mode = "full"
)

// AllModes lists every valid mode. Tests iterate it to prove exhaustiveness.
var AllModes = []Mode{ModeRecon, ModeAssess, ModeExploit, ModeFull}

// Valid reports whether the mode is one of the declared constants.
func (m Mode) Valid() bool {
	switch m {
	case ModeRecon, ModeAssess, ModeExploit, ModeFull:
		return true
	}
	return false
}

// AllowsExploitation reports whether PoC validation is in scope.
func (m Mode) AllowsExploitation() bool {
	return m == ModeExploit || m == ModeFull
}

// AllowsPostExploitation reports whether pivoting and escalation are in scope.
func (m Mode) AllowsPostExploitation() bool { return m == ModeFull }

// Stages returns the ordered stages this mode executes.
func (m Mode) Stages() []StageKind {
	switch m {
	case ModeRecon:
		return []StageKind{StageRecon, StageOSINT, StageReporting}
	case ModeAssess:
		return []StageKind{StageRecon, StageOSINT, StageAnalysis, StageMapper, StageReporting}
	case ModeExploit, ModeFull:
		return []StageKind{StageRecon, StageOSINT, StageAnalysis, StageMapper, StageExploit, StageReporting}
	}
	return nil
}

// IncludesStage reports whether the mode runs the given stage.
func (m Mode) IncludesStage(k StageKind) bool {
	for _, s := range m.Stages() {
		if s == k {
			return true
		}
	}
	return false
}
