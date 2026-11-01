package pipeline_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

const refNow = domain.TimestampMs(1735689600000)

// fixedClock returns a constant instant.
type fixedClock struct{ at domain.TimestampMs }

func (c fixedClock) Now() domain.TimestampMs { return c.at }

// recordedRun is one stage attempt the fake store observed.
type recordedRun struct {
	EngagementID domain.ID
	Stage        domain.StageKind
	Status       domain.StageRunStatus
	Summary      string
}

// fakeStore records every persistence call the runner makes.
type fakeStore struct {
	mu sync.Mutex

	begun    []recordedRun
	finished []recordedRun
	advances int
	terminal []domain.Status
	failOn   map[domain.StageKind]error
	counter  int
}

func newFakeStore() *fakeStore {
	return &fakeStore{failOn: make(map[domain.StageKind]error)}
}

func (s *fakeStore) AdvanceStage(_ context.Context, _ domain.ID, _, _ domain.Status, _ domain.EngageStage, _ domain.TimestampMs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advances++
	return nil
}

func (s *fakeStore) BeginStageRun(_ context.Context, engagementID domain.ID, stage domain.StageKind, _ domain.TimestampMs) (pipeline.StageRunRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err, ok := s.failOn[stage]; ok {
		return pipeline.StageRunRef{}, err
	}

	s.counter++
	s.begun = append(s.begun, recordedRun{EngagementID: engagementID, Stage: stage})
	return pipeline.StageRunRef{ID: domain.NewULIDGenerator().New(), Stage: stage, Attempt: 1}, nil
}

func (s *fakeStore) FinishStageRun(_ context.Context, run pipeline.StageRunRef, status domain.StageRunStatus, summary string, _ domain.TimestampMs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, recordedRun{Stage: run.Stage, Status: status, Summary: summary})
	return nil
}

func (s *fakeStore) RecordTerminal(_ context.Context, _ domain.ID, status domain.Status, _ domain.TimestampMs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminal = append(s.terminal, status)
	return nil
}

// begunStages lists the attempts in order.
func (s *fakeStore) begunStages() []domain.StageKind {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]domain.StageKind, 0, len(s.begun))
	for _, run := range s.begun {
		out = append(out, run.Stage)
	}
	return out
}

// stubStage is a configurable stage implementation.
type stubStage struct {
	kind     domain.StageKind
	enterErr error
	bodyErr  error
	exit     pipeline.ExitResult
	entered  bool
	bodied   bool
}

func (s *stubStage) Kind() domain.StageKind { return s.kind }

func (s *stubStage) Enter(_ context.Context, _ *pipeline.State) error {
	s.entered = true
	return s.enterErr
}

func (s *stubStage) Body(_ context.Context, _ *pipeline.State) error {
	s.bodied = true
	return s.bodyErr
}

func (s *stubStage) Exit(_ context.Context, _ *pipeline.State) pipeline.ExitResult { return s.exit }

// passingStage returns a stage that runs and passes.
func passingStage(kind domain.StageKind) *stubStage {
	return &stubStage{kind: kind, exit: pipeline.MetWith()}
}

// engagementFixture builds a draft engagement in the given mode.
func engagementFixture(t *testing.T, mode domain.Mode) domain.Engagement {
	t.Helper()

	engagement, err := domain.NewEngagement(domain.NewEngagementParams{
		Name: "pipeline-test",
		Mode: mode,
		Authorization: domain.Authorization{
			AuthorizedBy: "tester",
			AuthorizedAt: refNow,
		},
		Targets: []domain.Target{{Kind: domain.TargetDomain, Value: "example.test"}},
		Now:     refNow,
	})
	require.NoError(t, err)

	engagement.ID = domain.NewULIDGenerator().New()
	return engagement
}
