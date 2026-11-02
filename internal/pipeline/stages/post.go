package stages

import (
	"context"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

// Sentinel errors for the post-exploitation stage.
var (
	ErrNoAccessSession = fmt.Errorf("post: no access session and no verified finding")
	ErrCleanupMissing  = fmt.Errorf("post: a session has no recorded cleanup")
)

// Session is one live foothold.
type Session struct {
	ID           domain.ID
	State        string
	Host         string
	TechniqueIDs []string
}

// PostAction is one post-exploitation step.
type PostAction struct {
	SessionID    domain.ID
	Kind         string
	TechniqueIDs []string
	Cleanup      bool
}

// PostRecorder reads and writes the post-exploitation state.
type PostRecorder interface {
	// OpenSessions returns sessions that are still open, oldest first.
	OpenSessions(ctx context.Context, engagementID domain.ID) ([]Session, error)

	// VerifiedFindings counts findings proven by evidence.
	VerifiedFindings(ctx context.Context, engagementID domain.ID) (int, error)

	// RecordAction persists one action and returns its identifier.
	RecordAction(ctx context.Context, action PostAction) (domain.ID, error)

	// HasCleanup reports whether a session already has a cleanup action.
	HasCleanup(ctx context.Context, sessionID domain.ID) (bool, error)

	// Close marks a session closed.
	Close(ctx context.Context, sessionID domain.ID) error
}

// ActionRunner performs one post-exploitation action.
type ActionRunner interface {
	Run(ctx context.Context, action PostAction) error
}

// PostDeps configures the stage.
type PostDeps struct {
	Sessions PostRecorder
	Runner   ActionRunner
	Clock    domain.Clock

	// Actions are the steps applied to each open session, in order.
	Actions []PostAction
}

// PostStage records post-exploitation activity and always leaves a cleanup step.
type PostStage struct {
	deps PostDeps
}

// NewPostStage validates the dependencies.
func NewPostStage(deps PostDeps) (*PostStage, error) {
	if deps.Sessions == nil {
		return nil, fmt.Errorf("post stage: sessions: %w", pipeline.ErrStageMissing)
	}
	if deps.Runner == nil {
		return nil, fmt.Errorf("post stage: runner: %w", pipeline.ErrStageMissing)
	}
	if deps.Clock == nil {
		return nil, fmt.Errorf("post stage: clock: %w", pipeline.ErrStageMissing)
	}
	return &PostStage{deps: deps}, nil
}

// Kind identifies the stage.
//
// Post-exploitation has no stage of its own in domain.StageKind: the specification
// models it as the tail of the exploit stage, which is why this reports the same
// kind. A seventh stage would need a schema and a migration the roadmap does not
// call for.
func (s *PostStage) Kind() domain.StageKind { return domain.StageExploit }

// Enter requires a foothold or a proven finding.
func (s *PostStage) Enter(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: post state", pipeline.ErrNoState)
	}

	open, err := s.deps.Sessions.OpenSessions(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("read open sessions: %w", err)
	}
	if len(open) > 0 {
		return nil
	}

	verified, err := s.deps.Sessions.VerifiedFindings(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("count verified findings: %w", err)
	}
	if verified == 0 {
		return fmt.Errorf("no open session and no verified finding: %w", ErrNoAccessSession)
	}
	return nil
}

// Body applies the configured actions to each open session, then records cleanup.
func (s *PostStage) Body(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: post state", pipeline.ErrNoState)
	}

	sessions, err := s.deps.Sessions.OpenSessions(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("read open sessions: %w", err)
	}

	for _, session := range sessions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.runSession(ctx, session); err != nil {
			return err
		}
	}
	return nil
}

// runSession applies the action list and guarantees a trailing cleanup.
func (s *PostStage) runSession(ctx context.Context, session Session) error {
	for _, template := range s.deps.Actions {
		action := template
		action.SessionID = session.ID

		if err := s.deps.Runner.Run(ctx, action); err != nil {
			return fmt.Errorf("run %s on session %s: %w", action.Kind, session.ID, err)
		}
		if _, err := s.deps.Sessions.RecordAction(ctx, action); err != nil {
			return fmt.Errorf("record %s on session %s: %w", action.Kind, session.ID, err)
		}
	}

	hasCleanup, err := s.deps.Sessions.HasCleanup(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("read cleanup state for %s: %w", session.ID, err)
	}
	if hasCleanup {
		return nil
	}

	cleanup := PostAction{
		SessionID: session.ID,
		Kind:      "cleanup",
		Cleanup:   true,
	}
	if _, err := s.deps.Sessions.RecordAction(ctx, cleanup); err != nil {
		return fmt.Errorf("record cleanup for %s: %w", session.ID, err)
	}
	return nil
}

// Exit is satisfied when every session is closed or has a cleanup action.
func (s *PostStage) Exit(ctx context.Context, state *pipeline.State) pipeline.ExitResult {
	if state == nil {
		return pipeline.Unmet("post state is missing")
	}

	open, err := s.deps.Sessions.OpenSessions(ctx, state.Engagement.ID)
	if err != nil {
		return pipeline.Unmet("cannot read open sessions: %v", err)
	}
	if len(open) == 0 {
		return pipeline.MetWith()
	}

	for _, session := range open {
		hasCleanup, err := s.deps.Sessions.HasCleanup(ctx, session.ID)
		if err != nil {
			return pipeline.Unmet("cannot read cleanup state for %s: %v", session.ID, err)
		}
		if !hasCleanup {
			return pipeline.Unmet("session %s is open and has no recorded cleanup", session.ID)
		}
	}
	return pipeline.MetWith()
}
