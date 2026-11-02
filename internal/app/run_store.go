package app

import (
	"context"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/store"
)

// runStore adapts the engagement and stage-run repositories to pipeline.RunStore.
//
// It lives here rather than in store because it has to satisfy a port defined by
// pipeline, and making store import pipeline would invert the dependency direction
// the architecture fixes. app already depends on both, so the adapter belongs at
// the composition boundary.
type runStore struct {
	engagements *store.EngagementRepo
	stageRuns   *store.StageRunRepo
	ids         domain.IDGenerator
}

// newRunStore returns the adapter over one database.
func newRunStore(db *store.DB) *runStore {
	return &runStore{
		engagements: store.NewEngagementRepo(db),
		stageRuns:   store.NewStageRunRepo(db),
		ids:         domain.NewULIDGenerator(),
	}
}

// AdvanceStage compare-and-sets the engagement's status and stage pointer.
func (s *runStore) AdvanceStage(
	ctx context.Context,
	id domain.ID,
	from, to domain.Status,
	stage domain.EngageStage,
	at domain.TimestampMs,
) error {
	if err := s.engagements.UpdateStatus(ctx, id, from, to, stage, at); err != nil {
		return fmt.Errorf("advance stage: %w", err)
	}
	return nil
}

// BeginStageRun opens an attempt and returns its reference.
//
// The attempt number comes from the repository rather than a process-local counter,
// so a restart continues the numbering instead of starting again at one.
func (s *runStore) BeginStageRun(
	ctx context.Context,
	engagementID domain.ID,
	stage domain.StageKind,
	at domain.TimestampMs,
) (pipeline.StageRunRef, error) {
	run, err := s.stageRuns.Begin(ctx, engagementID, stage, at, s.ids.New())
	if err != nil {
		return pipeline.StageRunRef{}, fmt.Errorf("begin stage run: %w", err)
	}

	return pipeline.StageRunRef{ID: run.ID, Stage: run.Stage, Attempt: run.Attempt}, nil
}

// FinishStageRun closes an attempt with its outcome.
func (s *runStore) FinishStageRun(
	ctx context.Context,
	ref pipeline.StageRunRef,
	status domain.StageRunStatus,
	summary string,
	at domain.TimestampMs,
) error {
	run := store.StageRun{
		ID:         ref.ID,
		Stage:      ref.Stage,
		Attempt:    ref.Attempt,
		Status:     status,
		Summary:    summary,
		FinishedAt: at,
	}

	if err := s.stageRuns.Finish(ctx, run, at); err != nil {
		return fmt.Errorf("finish stage run: %w", err)
	}
	return nil
}

// RecordTerminal writes the engagement's terminal status.
//
// It reads the current status first because the repository's move is a
// compare-and-set: passing a stale "from" would fail the guard, and passing the
// value read here is the only way to reach the intended terminal state through the
// single audited path.
func (s *runStore) RecordTerminal(ctx context.Context, id domain.ID, status domain.Status, at domain.TimestampMs) error {
	engagement, err := s.engagements.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read engagement %s: %w", id, err)
	}

	if err := s.engagements.UpdateStatus(ctx, id, engagement.Status, status, engagement.Stage, at); err != nil {
		return fmt.Errorf("record terminal status: %w", err)
	}
	return nil
}
