// Package pipeline orchestrates the six engagement stages. Each stage is a
// deterministic procedure: it validates preconditions, runs a bounded
// LLM-assisted body, and then asks a database predicate whether it is finished.
// The model may propose completion; only the predicate decides.
package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors surfaced by stage execution.
var (
	ErrExitCriteriaUnmet = errors.New("pipeline: exit criteria not met")
	ErrIterationCap      = errors.New("pipeline: iteration cap reached")
	ErrBudgetExhausted   = errors.New("pipeline: budget exhausted")
	ErrStageNotAllowed   = errors.New("pipeline: stage excluded by engagement mode")
	ErrNoState           = errors.New("pipeline: stage state is missing")
)

// Stage is one deterministic phase of the engagement.
type Stage interface {
	// Kind identifies the stage.
	Kind() domain.StageKind

	// Enter validates preconditions before any work happens.
	Enter(ctx context.Context, state *State) error

	// Body runs the bounded work loop.
	Body(ctx context.Context, state *State) error

	// Exit is the sole authority on completion. It reads persisted state, never
	// in-memory progress, so it survives a restart.
	Exit(ctx context.Context, state *State) ExitResult
}

// ExitResult is the verdict of an exit predicate.
type ExitResult struct {
	// Met reports whether the stage may advance.
	Met bool

	// Reason names what is missing when Met is false. It must be specific enough
	// for an operator to act on; "criteria not met" is a defect.
	Reason string
}

// MetWith returns a satisfied verdict.
func MetWith() ExitResult { return ExitResult{Met: true} }

// Unmet returns an unsatisfied verdict with an actionable reason.
func Unmet(format string, args ...any) ExitResult {
	return ExitResult{Met: false, Reason: fmt.Sprintf(format, args...)}
}

// State is the mutable context handed to a stage for one attempt.
//
// It carries identifiers only. Every fact the stage needs is read from storage
// through the injected dependencies, which is what makes a run resumable.
type State struct {
	Engagement domain.Engagement
	Run        StageRunRef
	Budget     Budget
	Clock      domain.Clock
}

// StageRunRef identifies the persisted stage run this attempt writes to.
type StageRunRef struct {
	ID      domain.ID
	Stage   domain.StageKind
	Attempt int
}

// Validate rejects a state missing a required identifier.
func (s State) Validate() error {
	if s.Run.ID.IsZero() {
		return fmt.Errorf("run id: %w", ErrNoState)
	}
	if !s.Run.Stage.Valid() {
		return fmt.Errorf("run stage %q: %w", s.Run.Stage, ErrNoState)
	}
	if s.Clock == nil {
		return fmt.Errorf("clock: %w", ErrNoState)
	}
	return nil
}

// Budget bounds one stage's consumption.
type Budget struct {
	IterationsUsed int
	IterationsMax  int
	TokensUsed     int64
	TokensMax      int64
	CostMicrosUsed int64
	CostMicrosMax  int64
}

// Exhausted reports whether any dimension is spent.
func (b Budget) Exhausted() bool {
	if b.IterationsMax > 0 && b.IterationsUsed >= b.IterationsMax {
		return true
	}
	if b.TokensMax > 0 && b.TokensUsed >= b.TokensMax {
		return true
	}
	if b.CostMicrosMax > 0 && b.CostMicrosUsed >= b.CostMicrosMax {
		return true
	}
	return false
}

// Warn returns true when any dimension has crossed its warning threshold.
func (b Budget) Warn() bool {
	const warnRatio = 0.8

	if b.IterationsMax > 0 && float64(b.IterationsUsed) >= warnRatio*float64(b.IterationsMax) {
		return true
	}
	if b.TokensMax > 0 && float64(b.TokensUsed) >= warnRatio*float64(b.TokensMax) {
		return true
	}
	if b.CostMicrosMax > 0 && float64(b.CostMicrosUsed) >= warnRatio*float64(b.CostMicrosMax) {
		return true
	}
	return false
}

// Allowed reports whether the engagement mode permits this stage.
func Allowed(mode domain.Mode, kind domain.StageKind) bool {
	return mode.IncludesStage(kind)
}
