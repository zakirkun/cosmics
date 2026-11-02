package app

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/tui"
)

// submitter turns one target prompt into a finished run.
//
// The save, the start and the pipeline share one command because they belong to
// one user action: typing a target means "test this host". Reporting the finished
// engagement rather than the draft is what lets the dashboard show the outcome.
type submitter struct {
	db   *store.DB
	send func(tea.Msg)
	run  func(ctx context.Context, engagement domain.Engagement, progress func(domain.StageKind)) (domain.Engagement, error)
}

// OnRun persists a draft, reports it immediately, then drives it.
func (s *submitter) OnRun(engagement domain.Engagement, targets []domain.Target) tea.Cmd {
	engagement.ID = domain.NewULIDGenerator().New()

	return func() tea.Msg {
		ctx := context.Background()

		repo := store.NewEngagementRepo(s.db)
		if err := repo.Create(ctx, engagement, targets); err != nil {
			return tui.RunFinishedMsg{Engagement: engagement, Err: fmt.Errorf("save engagement: %w", err)}
		}

		saved, err := repo.Get(ctx, engagement.ID)
		if err != nil {
			return tui.RunFinishedMsg{Engagement: engagement, Err: fmt.Errorf("reload engagement: %w", err)}
		}

		return s.drive(ctx, saved)
	}
}

// RunExisting drives an engagement that already exists.
//
// This is the "status is draft, run it now" path: the row is already durable, so
// nothing is inserted and the only decision left is whether its status permits a
// run.
func (s *submitter) RunExisting(engagement domain.Engagement) tea.Cmd {
	return func() tea.Msg {
		return s.drive(context.Background(), engagement)
	}
}

// OnDraft keeps the draft-only path for a caller that wants to file without running.
func (s *submitter) OnDraft(engagement domain.Engagement, targets []domain.Target) tea.Cmd {
	engagement.ID = domain.NewULIDGenerator().New()

	return func() tea.Msg {
		ctx := context.Background()
		repo := store.NewEngagementRepo(s.db)

		if err := repo.Create(ctx, engagement, targets); err != nil {
			return tui.ErrorMsg{Err: fmt.Errorf("save engagement: %w", err)}
		}

		reloaded, err := repo.List(ctx)
		if err != nil {
			return tui.ErrorMsg{Err: fmt.Errorf("reload engagements: %w", err)}
		}
		return tui.EngagementsLoadedMsg{Engagements: reloaded}
	}
}

// drive reports the start, runs the pipeline and reloads the final row.
func (s *submitter) drive(ctx context.Context, engagement domain.Engagement) tea.Msg {
	if s.send != nil {
		s.send(tui.RunStartedMsg{Engagement: engagement})
	}

	progress := func(stage domain.StageKind) {
		if s.send != nil {
			s.send(tui.RunProgressMsg{EngagementID: engagement.ID.String(), Stage: stage})
		}
	}

	finished, runErr := s.run(ctx, engagement, progress)

	// The reload runs on a detached context: the run context may already be
	// cancelled, and the operator still needs to see the terminal state that was
	// written.
	reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if reloaded, err := store.NewEngagementRepo(s.db).Get(reloadCtx, engagement.ID); err == nil {
		finished = reloaded
	}
	if runErr != nil {
		return tui.RunFinishedMsg{Engagement: finished, Err: fmt.Errorf("run engagement: %w", runErr)}
	}
	return tui.RunFinishedMsg{Engagement: finished}
}
