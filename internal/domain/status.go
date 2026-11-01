package domain

import (
	"fmt"
	"strings"
)

// Status is the lifecycle state of an engagement.
type Status string

// Engagement states.
const (
	StatusDraft     Status = "draft"
	StatusReady     Status = "ready"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusAborted   Status = "aborted"
)

// AllStatuses lists every declared status, for exhaustiveness tests.
var AllStatuses = []Status{
	StatusDraft, StatusReady, StatusRunning, StatusPaused,
	StatusCompleted, StatusFailed, StatusAborted,
}

// Valid reports whether the status is a declared constant.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusReady, StatusRunning, StatusPaused,
		StatusCompleted, StatusFailed, StatusAborted:
		return true
	}
	return false
}

// Terminal reports whether the engagement has stopped for good.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusAborted:
		return true
	case StatusDraft, StatusReady, StatusRunning, StatusPaused:
		return false
	}
	return false
}

// allowedTransitions is the single source of truth for the engagement state
// machine. A transition absent from this map is rejected and audited.
var allowedTransitions = map[Status][]Status{
	StatusDraft:     {StatusReady},
	StatusReady:     {StatusRunning, StatusAborted},
	StatusRunning:   {StatusPaused, StatusCompleted, StatusFailed, StatusAborted},
	StatusPaused:    {StatusRunning, StatusAborted},
	StatusCompleted: {StatusReady},
	StatusFailed:    {StatusReady},
	StatusAborted:   {StatusReady},
}

// CanTransitionTo reports whether moving from s to next is legal.
func (s Status) CanTransitionTo(next Status) bool {
	for _, allowed := range allowedTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Transition validates a move and returns a typed error naming both sides.
func (s Status) Transition(next Status) error {
	if !s.Valid() {
		return fmt.Errorf("status %q: %w", s, ErrUnknownStatus)
	}
	if !next.Valid() {
		return fmt.Errorf("status %q: %w", next, ErrUnknownStatus)
	}
	if !s.CanTransitionTo(next) {
		return fmt.Errorf("%s -> %s: %w", s, next, ErrIllegalMove)
	}
	return nil
}

// EngageStage is the pipeline pointer. It is distinct from Status: an
// engagement can be paused mid-analysis without losing that position.
type EngageStage string

// Pipeline pointers stored on the engagement row.
const (
	EngageStageNone      EngageStage = "none"
	EngageStageRecon     EngageStage = "recon"
	EngageStageOSINT     EngageStage = "osint"
	EngageStageAnalysis  EngageStage = "analysis"
	EngageStageMapper    EngageStage = "mapper"
	EngageStageExploit   EngageStage = "exploit"
	EngageStageReporting EngageStage = "reporting"
	EngageStageDone      EngageStage = "done"
)

// AllEngageStages lists every pipeline pointer value.
var AllEngageStages = []EngageStage{
	EngageStageNone,
	EngageStageRecon,
	EngageStageOSINT,
	EngageStageAnalysis,
	EngageStageMapper,
	EngageStageExploit,
	EngageStageReporting,
	EngageStageDone,
}

// Valid reports whether the pointer is a declared constant.
func (e EngageStage) Valid() bool {
	for _, candidate := range AllEngageStages {
		if candidate == e {
			return true
		}
	}
	return false
}

// StageKind converts the pointer to a stage, or false for none/done.
func (e EngageStage) StageKind() (StageKind, bool) {
	switch e {
	case EngageStageRecon:
		return StageRecon, true
	case EngageStageOSINT:
		return StageOSINT, true
	case EngageStageAnalysis:
		return StageAnalysis, true
	case EngageStageMapper:
		return StageMapper, true
	case EngageStageExploit:
		return StageExploit, true
	case EngageStageReporting:
		return StageReporting, true
	case EngageStageNone, EngageStageDone:
		return "", false
	}
	return "", false
}

// EngageStageOf converts a stage into its pipeline pointer.
func EngageStageOf(k StageKind) EngageStage {
	switch k {
	case StageRecon:
		return EngageStageRecon
	case StageOSINT:
		return EngageStageOSINT
	case StageAnalysis:
		return EngageStageAnalysis
	case StageMapper:
		return EngageStageMapper
	case StageExploit:
		return EngageStageExploit
	case StageReporting:
		return EngageStageReporting
	}
	return EngageStageNone
}

// Authorization is the immutable record that an engagement was sanctioned.
// It is written once at creation and protected by a database trigger.
type Authorization struct {
	AuthorizedBy string
	Note         string
	AuthorizedAt TimestampMs
}

// Validate enforces the acknowledgement contract: a named authorizing party and
// a recorded instant are both mandatory.
func (a Authorization) Validate() error {
	if strings.TrimSpace(a.AuthorizedBy) == "" {
		return fmt.Errorf("authorized_by: %w", ErrUnauthorized)
	}
	if a.AuthorizedAt.IsZero() {
		return fmt.Errorf("authorized_at: %w", ErrUnauthorized)
	}
	return nil
}

// maxNameLength bounds human-supplied labels stored in indexed columns.
const maxNameLength = 200

// ValidateName checks an engagement or artifact name.
func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ErrEmptyName
	}
	if len(trimmed) > maxNameLength {
		return fmt.Errorf("name %q: %w", trimmed, ErrNameTooLong)
	}
	return nil
}
