package domain

import (
	"fmt"
	"strings"
)

// StageKind is one of the six ordered pipeline stages.
type StageKind string

// The pipeline stages, in execution order.
const (
	StageRecon     StageKind = "recon"
	StageOSINT     StageKind = "osint"
	StageAnalysis  StageKind = "analysis"
	StageMapper    StageKind = "mapper"
	StageExploit   StageKind = "exploit"
	StageReporting StageKind = "reporting"
)

// AllStages is the canonical execution order.
var AllStages = []StageKind{
	StageRecon,
	StageOSINT,
	StageAnalysis,
	StageMapper,
	StageExploit,
	StageReporting,
}

// Valid reports whether the stage is one of the declared constants.
func (s StageKind) Valid() bool {
	switch s {
	case StageRecon, StageOSINT, StageAnalysis, StageMapper, StageExploit, StageReporting:
		return true
	}
	return false
}

// Title returns the human-facing stage name.
func (s StageKind) Title() string {
	switch s {
	case StageRecon:
		return "Recon"
	case StageOSINT:
		return "OSINT"
	case StageAnalysis:
		return "Analysis"
	case StageMapper:
		return "Mapper"
	case StageExploit:
		return "Exploit"
	case StageReporting:
		return "Reporting"
	}
	return string(s)
}

// Next returns the following stage in canonical order, or false at the end.
func (s StageKind) Next() (StageKind, bool) {
	for i, candidate := range AllStages {
		if candidate == s && i+1 < len(AllStages) {
			return AllStages[i+1], true
		}
	}
	return "", false
}

// Position is the 1-based index of the stage, or 0 when unknown.
func (s StageKind) Position() int {
	for i, candidate := range AllStages {
		if candidate == s {
			return i + 1
		}
	}
	return 0
}

// StageRunStatus is the lifecycle of one attempt at a stage.
type StageRunStatus string

// Stage run states.
const (
	StagePending   StageRunStatus = "pending"
	StageRunning   StageRunStatus = "running"
	StageSucceeded StageRunStatus = "succeeded"
	StageFailed    StageRunStatus = "failed"
	StageSkipped   StageRunStatus = "skipped"
	StageAborted   StageRunStatus = "aborted"
)

// Valid reports whether the status is a declared constant.
func (s StageRunStatus) Valid() bool {
	switch s {
	case StagePending, StageRunning, StageSucceeded, StageFailed, StageSkipped, StageAborted:
		return true
	}
	return false
}

// Terminal reports whether no further transition is legal.
func (s StageRunStatus) Terminal() bool {
	switch s {
	case StageSucceeded, StageFailed, StageSkipped, StageAborted:
		return true
	case StagePending, StageRunning:
		return false
	}
	return false
}

// AuditAction enumerates the operations recorded in the audit trail.
type AuditAction string

// Audit actions. These strings are persisted, so they are never renamed.
const (
	AuditEngagementCreated       AuditAction = "engagement_created"
	AuditAuthorizationRecorded   AuditAction = "authorization_recorded"
	AuditTargetAdded             AuditAction = "target_added"
	AuditEngagementStatusChanged AuditAction = "engagement_status_changed"
	AuditToolExecuted            AuditAction = "tool_executed"
	AuditExploitLaunched         AuditAction = "exploit_launched"
	AuditReportGenerated         AuditAction = "report_generated"
	AuditConfigChanged           AuditAction = "config_changed"
)

// Valid reports whether the action is a declared constant.
func (a AuditAction) Valid() bool {
	switch a {
	case AuditEngagementCreated, AuditAuthorizationRecorded, AuditTargetAdded,
		AuditEngagementStatusChanged, AuditToolExecuted, AuditExploitLaunched,
		AuditReportGenerated, AuditConfigChanged:
		return true
	}
	return false
}

// Actor identifies who performed an audited action.
type Actor string

// Audited actors.
const (
	ActorOperator Actor = "operator"
	ActorAgent    Actor = "agent"
	ActorSystem   Actor = "system"
)

// Valid reports whether the actor is a declared constant.
func (a Actor) Valid() bool {
	switch a {
	case ActorOperator, ActorAgent, ActorSystem:
		return true
	}
	return false
}

// AuditOutcome records whether an action was permitted.
type AuditOutcome string

// Audit outcomes.
const (
	OutcomeAllowed AuditOutcome = "allowed"
	OutcomeDenied  AuditOutcome = "denied"
	OutcomeError   AuditOutcome = "error"
)

// Valid reports whether the outcome is a declared constant.
func (o AuditOutcome) Valid() bool {
	switch o {
	case OutcomeAllowed, OutcomeDenied, OutcomeError:
		return true
	}
	return false
}

// TargetKind classifies a seeded target.
type TargetKind string

// Target kinds accepted at engagement creation.
const (
	TargetDomain TargetKind = "domain"
	TargetHost   TargetKind = "host"
	TargetIP     TargetKind = "ip"
	TargetCIDR   TargetKind = "cidr"
	TargetURL    TargetKind = "url"
	TargetRepo   TargetKind = "repo"
	TargetBinary TargetKind = "binary"
)

// Valid reports whether the kind is a declared constant.
func (k TargetKind) Valid() bool {
	switch k {
	case TargetDomain, TargetHost, TargetIP, TargetCIDR, TargetURL, TargetRepo, TargetBinary:
		return true
	}
	return false
}

// Target is one operator-supplied seed target.
type Target struct {
	ID           ID
	EngagementID ID
	Kind         TargetKind
	Value        string
	Raw          string
	Note         string
	CreatedAt    TimestampMs
}

// maxTargetLength bounds a target value to keep prompts and argv sane.
const maxTargetLength = 512

// NewTarget parses and normalises a seed target. It rejects empty values,
// control characters, whitespace-padded input, and anything containing a shell
// metacharacter, because target values reach container argv.
func NewTarget(kind TargetKind, raw string) (Target, error) {
	if !kind.Valid() {
		return Target{}, fmt.Errorf("target kind %q: %w", kind, ErrBadTarget)
	}

	value := strings.TrimSpace(raw)
	if value == "" {
		return Target{}, fmt.Errorf("target: %w", ErrBadTarget)
	}
	if len(value) > maxTargetLength {
		return Target{}, fmt.Errorf("target %q exceeds %d characters: %w", value, maxTargetLength, ErrBadTarget)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return Target{}, fmt.Errorf("target contains control character %q: %w", r, ErrBadTarget)
		}
		if isShellMetachar(r) {
			return Target{}, fmt.Errorf("target contains shell metacharacter %q: %w", r, ErrBadTarget)
		}
	}

	return Target{Kind: kind, Value: value, Raw: raw}, nil
}

// isShellMetachar reports whether a rune could alter a shell command line.
// Target values are passed as argv elements and never through a shell, but a
// metacharacter in a target is a mistake worth rejecting at the boundary.
func isShellMetachar(r rune) bool {
	switch r {
	case ';', '|', '&', '$', '`', '>', '<', '\n', '\r', '\\', '"', '\'':
		return true
	}
	return false
}
