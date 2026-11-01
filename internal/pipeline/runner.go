package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors surfaced by the runner.
var (
	ErrAlreadyTerminal = errors.New("pipeline: engagement is already terminal")
	ErrStageMissing    = errors.New("pipeline: stage implementation missing")
	ErrNoProgress      = errors.New("pipeline: stage made no progress")
)

// RunStore is the persistence the runner needs. It is a narrow interface so a
// test can drive the whole pipeline without a database.
type RunStore interface {
	// AdvanceStage compare-and-sets the engagement's status and stage pointer.
	AdvanceStage(ctx context.Context, id domain.ID, from, to domain.Status, stage domain.EngageStage, at domain.TimestampMs) error

	// BeginStageRun opens an attempt and returns its identifier.
	BeginStageRun(ctx context.Context, engagementID domain.ID, stage domain.StageKind, at domain.TimestampMs) (StageRunRef, error)

	// FinishStageRun closes an attempt.
	FinishStageRun(ctx context.Context, run StageRunRef, status domain.StageRunStatus, summary string, at domain.TimestampMs) error

	// RecordAdvance writes the terminal status and stage pointer.
	RecordTerminal(ctx context.Context, id domain.ID, status domain.Status, at domain.TimestampMs) error
}

// Runner executes the stage sequence for one engagement.
//
// It owns ordering and the state machine; each Stage owns its own work. A stage
// that returns nil has not necessarily finished — only its exit predicate decides
// that, and the runner asks it.
type Runner struct {
	stages map[domain.StageKind]Stage
	store  RunStore
	clock  domain.Clock
}

// RunnerOptions configures a Runner.
type RunnerOptions struct {
	Stages []Stage
	Store  RunStore
	Clock  domain.Clock
}

// NewRunner validates the stage set and returns a runner.
func NewRunner(opts RunnerOptions) (*Runner, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("store: %w", ErrStageMissing)
	}
	if opts.Clock == nil {
		return nil, fmt.Errorf("clock: %w", ErrStageMissing)
	}

	stages := make(map[domain.StageKind]Stage, len(opts.Stages))
	for _, stage := range opts.Stages {
		kind := stage.Kind()
		if !kind.Valid() {
			return nil, fmt.Errorf("stage %q: %w", kind, ErrStageMissing)
		}
		if _, duplicate := stages[kind]; duplicate {
			return nil, fmt.Errorf("stage %s registered twice: %w", kind, ErrStageMissing)
		}
		stages[kind] = stage
	}

	return &Runner{stages: stages, store: opts.Store, clock: opts.Clock}, nil
}

// Run executes every stage the engagement's mode allows, in canonical order.
//
// The loop stops on the first stage whose exit predicate is unmet after its body
// ran. Continuing past an unmet predicate is how a pipeline reports completion
// for an engagement that never actually finished its work.
func (r *Runner) Run(ctx context.Context, engagement domain.Engagement) (domain.Engagement, error) {
	if engagement.Status.Terminal() {
		return engagement, fmt.Errorf("engagement %s is %s: %w", engagement.ID, engagement.Status, ErrAlreadyTerminal)
	}

	current := engagement.Status
	if current == domain.StatusDraft {
		authorized, err := engagement.Authorize(r.clock.Now())
		if err != nil {
			return engagement, fmt.Errorf("authorize: %w", err)
		}
		engagement = authorized
		current = engagement.Status
	}

	if current == domain.StatusReady || current == domain.StatusPaused {
		started, err := engagement.Start(r.clock.Now())
		if err != nil {
			return engagement, fmt.Errorf("start: %w", err)
		}
		engagement = started
	}

	for _, kind := range engagement.Mode.Stages() {
		select {
		case <-ctx.Done():
			return engagement, fmt.Errorf("pipeline cancelled before %s: %w", kind, ctx.Err())
		default:
		}

		next, err := r.runStage(ctx, engagement, kind)
		engagement = next
		if err != nil {
			failed, failErr := engagement.Fail(r.clock.Now(), err)
			if failErr == nil {
				_ = r.store.RecordTerminal(ctx, engagement.ID, failed.Status, r.clock.Now())
				engagement = failed
			}
			return engagement, fmt.Errorf("stage %s: %w", kind, err)
		}
	}

	completed, err := engagement.Complete(r.clock.Now())
	if err != nil {
		return engagement, fmt.Errorf("complete: %w", err)
	}
	if err := r.store.RecordTerminal(ctx, engagement.ID, completed.Status, r.clock.Now()); err != nil {
		return engagement, fmt.Errorf("record completion: %w", err)
	}
	return completed, nil
}

// runStage executes one stage and advances the pointer when its predicate agrees.
func (r *Runner) runStage(ctx context.Context, engagement domain.Engagement, kind domain.StageKind) (domain.Engagement, error) {
	stage, ok := r.stages[kind]
	if !ok {
		return engagement, fmt.Errorf("no implementation for %s: %w", kind, ErrStageMissing)
	}

	now := r.clock.Now()

	run, err := r.store.BeginStageRun(ctx, engagement.ID, kind, now)
	if err != nil {
		return engagement, fmt.Errorf("begin stage run: %w", err)
	}

	state := &State{
		Engagement: engagement,
		Run:        run,
		Clock:      r.clock,
	}

	if err := stage.Enter(ctx, state); err != nil {
		_ = r.store.FinishStageRun(ctx, run, domain.StageFailed, err.Error(), r.clock.Now())
		return engagement, fmt.Errorf("enter: %w", err)
	}

	if err := stage.Body(ctx, state); err != nil {
		_ = r.store.FinishStageRun(ctx, run, domain.StageFailed, err.Error(), r.clock.Now())
		return engagement, fmt.Errorf("body: %w", err)
	}

	exit := stage.Exit(ctx, state)
	if !exit.Met {
		_ = r.store.FinishStageRun(ctx, run, domain.StageFailed, exit.Reason, r.clock.Now())
		return engagement, fmt.Errorf("%w: %s", ErrExitCriteriaUnmet, exit.Reason)
	}

	if err := r.store.FinishStageRun(ctx, run, domain.StageSucceeded, "", r.clock.Now()); err != nil {
		return engagement, fmt.Errorf("finish stage run: %w", err)
	}

	advanced, err := engagement.AdvanceTo(kind, r.clock.Now())
	if err != nil {
		return engagement, fmt.Errorf("advance pointer: %w", err)
	}

	if err := r.store.AdvanceStage(ctx, engagement.ID,
		engagement.Status, advanced.Status, advanced.Stage, r.clock.Now()); err != nil {
		return engagement, fmt.Errorf("persist advance: %w", err)
	}

	return advanced, nil
}

// StageKinds returns the registered stages in canonical order.
func (r *Runner) StageKinds() []domain.StageKind {
	out := make([]domain.StageKind, 0, len(r.stages))
	for _, kind := range domain.AllStages {
		if _, ok := r.stages[kind]; ok {
			out = append(out, kind)
		}
	}
	return out
}
