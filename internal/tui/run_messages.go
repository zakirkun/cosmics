package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

// RunStartedMsg reports that a draft was accepted and a run has begun.
//
// It is separate from EngagementDraftedMsg because the two answer different
// questions: drafted means "the row exists", started means "the pipeline is now
// driving it". A caller that only persists must not pretend the second happened.
type RunStartedMsg struct {
	Engagement domain.Engagement
}

// RunFinishedMsg reports that a run stopped, successfully or not.
type RunFinishedMsg struct {
	Engagement domain.Engagement
	Err        error
}

// RunProgressMsg reports that the pipeline entered a stage.
type RunProgressMsg struct {
	EngagementID string
	Stage        domain.StageKind
}

// handleRunStarted shows the newly created engagement immediately, before any
// stage reports progress, so pressing enter never looks like it did nothing.
func (m Model) handleRunStarted(msg RunStartedMsg) Model {
	m.err = nil
	m.engagements = upsertEngagement(m.engagements, msg.Engagement)
	m.cursor = indexOfEngagement(m.engagements, msg.Engagement.ID)
	m.screen = ScreenDashboard
	return m
}

// handleRunProgress moves the dashboard to the stage being worked on.
func (m Model) handleRunProgress(msg RunProgressMsg) Model {
	id, err := domain.NewID(msg.EngagementID)
	if err != nil {
		return m
	}

	index := indexOfEngagement(m.engagements, id)
	if index < 0 {
		return m
	}

	m.engagements[index].Stage = domain.EngageStage(msg.Stage)
	m.engagements[index].Status = domain.StatusRunning
	return m
}

// handleRunFinished records the terminal state and surfaces any failure.
func (m Model) handleRunFinished(msg RunFinishedMsg) Model {
	m.engagements = upsertEngagement(m.engagements, msg.Engagement)
	m.cursor = indexOfEngagement(m.engagements, msg.Engagement.ID)
	m.screen = ScreenDashboard

	if msg.Err != nil {
		m.err = msg.Err
	}
	return m
}

// upsertEngagement replaces an engagement in place, or appends it.
//
// Replacing rather than appending is what stops one run from producing two rows:
// the draft arrives first and every later message carries the same identifier.
func upsertEngagement(list []domain.Engagement, engagement domain.Engagement) []domain.Engagement {
	for index := range list {
		if list[index].ID.Equal(engagement.ID) {
			list[index] = engagement
			return list
		}
	}
	return append(list, engagement)
}

// indexOfEngagement returns the position of an identifier, or -1.
func indexOfEngagement(list []domain.Engagement, id domain.ID) int {
	for index := range list {
		if list[index].ID.Equal(id) {
			return index
		}
	}
	return -1
}

// screenIsRunnable reports whether pressing enter on this engagement starts a run.
//
// A draft is included: the runner authorizes and starts it, which is exactly what
// an operator means by pressing enter on a target they just typed.
func (m Model) screenIsRunnable(engagement domain.Engagement) bool {
	switch engagement.Status {
	case domain.StatusDraft, domain.StatusReady, domain.StatusPaused:
		return true
	case domain.StatusRunning, domain.StatusCompleted, domain.StatusFailed, domain.StatusAborted:
		return false
	}
	return false
}

// runCmd returns the command that starts a pipeline run.
//
// It is a no-op when the application supplied no runner: a shell without one is a
// read-only preview, and silently pretending to run would be worse than doing
// nothing visible.
func (m Model) runCmd(engagement domain.Engagement, targets []domain.Target) tea.Cmd {
	if m.opts.OnRun == nil {
		return func() tea.Msg { return EngagementDraftedMsg{Engagement: engagement} }
	}
	return m.opts.OnRun(engagement, targets)
}
