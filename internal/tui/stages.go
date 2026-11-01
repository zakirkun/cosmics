package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

// Stage glyphs. They are deliberately single-width so the sidebar keeps its
// alignment in every terminal.
const (
	glyphPending = "○"
	glyphCurrent = "◐"
	glyphDone    = "●"
	glyphFailed  = "✗"
	glyphSkipped = "–"
)

// stageState is the rendering state of one pipeline stage.
type stageState int

const (
	stageStatePending stageState = iota
	stageStateCurrent
	stageStateDone
	stageStateFailed
	stageStateSkipped
)

// glyph returns the display glyph for a state.
func (s stageState) glyph() string {
	switch s {
	case stageStateCurrent:
		return glyphCurrent
	case stageStateDone:
		return glyphDone
	case stageStateFailed:
		return glyphFailed
	case stageStateSkipped:
		return glyphSkipped
	case stageStatePending:
		return glyphPending
	}
	return glyphPending
}

// style returns the sidebar style for a state.
func (s stageState) style() lipgloss.Style {
	switch s {
	case stageStateCurrent:
		return styleStageCurrent
	case stageStateDone:
		return styleStageDone
	case stageStateFailed:
		return styleStageFailed
	case stageStateSkipped:
		return styleStageSkipped
	case stageStatePending:
		return styleStagePending
	}
	return styleStagePending
}

// stageStates maps an engagement onto one state per stage in
// domain.AllStages order.
//
// The mapping is total and pure, so a nil engagement (nothing selected) and
// every status/stage combination render deterministically:
//
//   - nil, or a draft/ready engagement with no stage pointer: all pending.
//   - stages the engagement's mode never runs: skipped.
//   - stages before the pointer: done.
//   - the pointer stage: current while running/paused, failed when the
//     engagement failed, done when it completed.
//   - stages after the pointer: pending.
func stageStates(eng *domain.Engagement) []stageState {
	states := make([]stageState, len(domain.AllStages))
	if eng == nil {
		return states
	}

	currentIndex, hasCurrent := stageIndex(eng)

	reachedPipeline := eng.Status == domain.StatusRunning ||
		eng.Status == domain.StatusPaused ||
		eng.Status == domain.StatusCompleted ||
		eng.Status == domain.StatusFailed ||
		eng.Status == domain.StatusAborted

	for i, stage := range domain.AllStages {
		if reachedPipeline && !eng.Mode.IncludesStage(stage) {
			states[i] = stageStateSkipped
			continue
		}
		if !hasCurrent {
			continue
		}
		switch {
		case i < currentIndex:
			states[i] = stageStateDone
		case i == currentIndex:
			states[i] = currentState(eng.Status)
		}
	}
	return states
}

// currentState is the state of the stage the pointer names.
func currentState(status domain.Status) stageState {
	switch status {
	case domain.StatusFailed:
		return stageStateFailed
	case domain.StatusCompleted:
		return stageStateDone
	case domain.StatusAborted:
		return stageStateFailed
	case domain.StatusDraft, domain.StatusReady, domain.StatusRunning, domain.StatusPaused:
		return stageStateCurrent
	}
	return stageStateCurrent
}

// stageIndex resolves the engagement's pipeline pointer to an index into
// domain.AllStages. A "none" pointer and a "done" pointer carry no index, but
// "done" means every stage is finished, handled by the caller separately.
func stageIndex(eng *domain.Engagement) (int, bool) {
	switch eng.Stage {
	case domain.EngageStageDone:
		return len(domain.AllStages), true
	case domain.EngageStageNone:
		return 0, false
	case domain.EngageStageRecon, domain.EngageStageOSINT, domain.EngageStageAnalysis,
		domain.EngageStageMapper, domain.EngageStageExploit, domain.EngageStageReporting:
		kind, ok := eng.Stage.StageKind()
		if !ok {
			return 0, false
		}
		position := kind.Position()
		if position == 0 {
			return 0, false
		}
		return position - 1, true
	}
	return 0, false
}

// renderStageSidebar renders the six-stage list for an engagement, truncated
// to width. It is pure: no terminal, no colour state, no I/O.
func renderStageSidebar(eng *domain.Engagement, width int) string {
	if width <= 0 {
		return ""
	}

	states := stageStates(eng)
	lines := make([]string, 0, len(domain.AllStages))

	for i, stage := range domain.AllStages {
		state := states[i]
		glyph := state.style().Render(state.glyph())
		label := state.style().Render(truncate(stage.Title(), width-2))

		line := glyph + " " + label
		if pad := width - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}
