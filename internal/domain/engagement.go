package domain

import (
	"fmt"
	"strings"
)

// Engagement is the root aggregate: one authorized assessment.
//
// It is a value type. Store implementations return copies and accept copies,
// so a mutation never leaks across goroutines.
type Engagement struct {
	ID      ID
	Name    string
	Client  string
	Mode    Mode
	Status  Status
	Stage   EngageStage
	Profile string // model profile key resolved from config

	Authorization Authorization

	BudgetUSDMicros int64
	SpentUSDMicros  int64
	TokenBudget     int64
	TokensUsed      int64

	CreatedAt  TimestampMs
	UpdatedAt  TimestampMs
	StartedAt  TimestampMs
	FinishedAt TimestampMs

	ConfigJSON string
	Err        string
}

// NewEngagementParams groups the inputs to NewEngagement. A struct rather than
// five positional parameters keeps the call site readable and prevents the
// authorization and the name from being swapped by mistake.
type NewEngagementParams struct {
	Name          string
	Client        string
	Mode          Mode
	Profile       string
	Authorization Authorization
	Targets       []Target
	BudgetMicros  int64
	TokenBudget   int64
	Now           TimestampMs
}

// NewEngagement validates and constructs a draft engagement.
//
// Every rule the database enforces with CHECK constraints is also enforced here,
// so an invalid engagement is rejected before it reaches SQL and the caller
// receives a typed error naming the problem.
func NewEngagement(p NewEngagementParams) (Engagement, error) {
	if err := ValidateName(p.Name); err != nil {
		return Engagement{}, fmt.Errorf("engagement name: %w", err)
	}
	if !p.Mode.Valid() {
		return Engagement{}, fmt.Errorf("mode %q: %w", p.Mode, ErrUnknownMode)
	}
	if err := p.Authorization.Validate(); err != nil {
		return Engagement{}, fmt.Errorf("engagement authorization: %w", err)
	}
	if len(p.Targets) == 0 {
		return Engagement{}, ErrNoTargets
	}
	if p.BudgetMicros < 0 {
		return Engagement{}, fmt.Errorf("budget_usd_micros %d: must not be negative", p.BudgetMicros)
	}
	if p.TokenBudget < 0 {
		return Engagement{}, fmt.Errorf("token_budget %d: must not be negative", p.TokenBudget)
	}

	profile := strings.TrimSpace(p.Profile)
	if profile == "" {
		profile = "default"
	}

	return Engagement{
		Name:            strings.TrimSpace(p.Name),
		Client:          strings.TrimSpace(p.Client),
		Mode:            p.Mode,
		Status:          StatusDraft,
		Stage:           EngageStageNone,
		Profile:         profile,
		Authorization:   p.Authorization,
		BudgetUSDMicros: p.BudgetMicros,
		TokenBudget:     p.TokenBudget,
		CreatedAt:       p.Now,
		UpdatedAt:       p.Now,
		ConfigJSON:      "{}",
	}, nil
}

// Authorize arms a draft engagement for execution. Only a draft may be
// authorized; re-authorizing would silently rewrite the audit record.
func (e Engagement) Authorize(now TimestampMs) (Engagement, error) {
	if e.Status != StatusDraft {
		return Engagement{}, fmt.Errorf("authorize from %s: %w", e.Status, ErrIllegalMove)
	}
	e.Status = StatusReady
	e.UpdatedAt = now
	return e, nil
}

// Start moves a ready or paused engagement into running and points the stage
// pointer at the first stage the mode allows.
func (e Engagement) Start(now TimestampMs) (Engagement, error) {
	if err := e.Status.Transition(StatusRunning); err != nil {
		return Engagement{}, err
	}
	stages := e.Mode.Stages()
	if len(stages) == 0 {
		return Engagement{}, fmt.Errorf("mode %q has no stages: %w", e.Mode, ErrUnknownMode)
	}

	e.Status = StatusRunning
	e.UpdatedAt = now
	if e.StartedAt.IsZero() {
		e.StartedAt = now
	}
	if e.Stage == EngageStageNone {
		e.Stage = EngageStageOf(stages[0])
	}
	return e, nil
}

// AdvanceTo moves the pipeline pointer forward. Re-running the current stage is
// expressed by re-running its attempt, not by moving backwards.
func (e Engagement) AdvanceTo(next StageKind, now TimestampMs) (Engagement, error) {
	if !next.Valid() {
		return Engagement{}, fmt.Errorf("stage %q: %w", next, ErrUnknownStage)
	}
	if e.Status != StatusRunning {
		return Engagement{}, fmt.Errorf("advance while %s: %w", e.Status, ErrIllegalMove)
	}
	if !e.Mode.IncludesStage(next) {
		return Engagement{}, fmt.Errorf("stage %s excluded by mode %s: %w", next, e.Mode, ErrIllegalMove)
	}

	e.Stage = EngageStageOf(next)
	e.UpdatedAt = now
	return e, nil
}

// Complete closes a running engagement after Reporting.
func (e Engagement) Complete(now TimestampMs) (Engagement, error) {
	if err := e.Status.Transition(StatusCompleted); err != nil {
		return Engagement{}, err
	}
	e.Status = StatusCompleted
	e.Stage = EngageStageDone
	e.FinishedAt = now
	e.UpdatedAt = now
	return e, nil
}

// Fail records an unrecoverable error.
func (e Engagement) Fail(now TimestampMs, cause error) (Engagement, error) {
	if err := e.Status.Transition(StatusFailed); err != nil {
		return Engagement{}, err
	}
	e.Status = StatusFailed
	e.FinishedAt = now
	e.UpdatedAt = now
	if cause != nil {
		e.Err = cause.Error()
	}
	return e, nil
}

// Pause suspends execution while preserving the stage pointer.
func (e Engagement) Pause(now TimestampMs) (Engagement, error) {
	if err := e.Status.Transition(StatusPaused); err != nil {
		return Engagement{}, err
	}
	e.Status = StatusPaused
	e.UpdatedAt = now
	return e, nil
}

// Resume returns a paused engagement to running.
func (e Engagement) Resume(now TimestampMs) (Engagement, error) {
	if err := e.Status.Transition(StatusRunning); err != nil {
		return Engagement{}, err
	}
	e.Status = StatusRunning
	e.UpdatedAt = now
	return e, nil
}
