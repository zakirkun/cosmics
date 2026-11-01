package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_renderStageSidebar_renders_all_stages_as_pending_when_the_engagement_is_nil(t *testing.T) {
	// Given / When
	got := renderStageSidebar(nil, 24)

	// Then
	require.NotEmpty(t, got)
	for _, stage := range domain.AllStages {
		assert.Contains(t, got, stage.Title())
	}
	assert.Equal(t, len(domain.AllStages), strings.Count(got, "\n")+1)
	assert.NotContains(t, got, glyphDone)
	assert.NotContains(t, got, glyphCurrent)
}

func Test_renderStageSidebar_returns_an_empty_string_when_the_width_is_zero(t *testing.T) {
	// Given / When
	got := renderStageSidebar(nil, 0)

	// Then
	assert.Empty(t, got)
}

func Test_renderStageSidebar_marks_the_current_stage_when_an_engagement_is_running(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageMapper)

	// When
	got := renderStageSidebar(&eng, 24)

	// Then
	assert.Contains(t, got, glyphCurrent)
	assert.Equal(t, 1, strings.Count(got, glyphCurrent), "exactly one stage is current")
}

func Test_renderStageSidebar_marks_completed_stages_before_the_current_one(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageMapper)

	// When
	got := renderStageSidebar(&eng, 24)

	// Then
	mapperPosition := domain.StageMapper.Position()
	assert.Equal(t, mapperPosition-1, strings.Count(got, glyphDone),
		"every stage before mapper should be done")
}

func Test_renderStageSidebar_marks_stages_after_the_current_one_as_pending(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageAnalysis)

	// When
	got := renderStageSidebar(&eng, 24)

	// Then
	analysisPosition := domain.StageAnalysis.Position()
	afterCount := len(domain.AllStages) - analysisPosition
	assert.Equal(t, afterCount, strings.Count(got, glyphPending),
		"every stage after analysis should be pending")
}

func Test_renderStageSidebar_marks_unreached_stages_as_skipped_when_the_mode_excludes_them(t *testing.T) {
	// Given — recon mode never analyses, maps, or exploits
	eng := runningAtWithMode(t, domain.ModeRecon, domain.StageRecon)

	// When
	got := renderStageSidebar(&eng, 24)

	// Then
	assert.Contains(t, got, glyphSkipped)
	assert.NotContains(t, got, glyphDone)
}

func Test_renderStageSidebar_marks_the_pointer_as_failed_when_the_engagement_failed(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageRecon)
	failed, err := eng.Fail(domain.TimestampMs(1735689700000), assert.AnError)
	require.NoError(t, err)

	// When
	got := renderStageSidebar(&failed, 24)

	// Then
	assert.Contains(t, got, glyphFailed)
}

func Test_renderStageSidebar_marks_every_stage_done_when_the_engagement_completed(t *testing.T) {
	// Given
	eng := runningAtWithMode(t, domain.ModeFull, domain.StageReporting)
	completed, err := eng.Complete(domain.TimestampMs(1735689800000))
	require.NoError(t, err)
	require.Equal(t, domain.EngageStageDone, completed.Stage)

	// When
	got := renderStageSidebar(&completed, 24)

	// Then
	assert.Equal(t, len(domain.AllStages), strings.Count(got, glyphDone),
		"every stage must render as done after completion")
	assert.NotContains(t, got, glyphCurrent)
}

func Test_renderStageSidebar_truncates_labels_when_the_width_is_narrow(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageRecon)

	// When
	got := renderStageSidebar(&eng, 8)

	// Then
	for _, line := range strings.Split(got, "\n") {
		assert.LessOrEqual(t, len([]rune(stripANSI(line))), 8, "line %q exceeded the width", line)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false

	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Test_stageStates_returns_one_state_per_stage_when_given_an_engagement(t *testing.T) {
	// Given
	eng := runningAt(t, domain.StageMapper)

	// When
	states := stageStates(&eng)

	// Then
	require.Len(t, states, len(domain.AllStages))
}

func Test_stageStates_returns_all_pending_when_the_engagement_is_nil(t *testing.T) {
	// Given / When
	states := stageStates(nil)

	// Then
	require.Len(t, states, len(domain.AllStages))
	for i, state := range states {
		assert.Equal(t, stageStatePending, state, "stage %d", i)
	}
}

func Test_stageStates_treats_a_draft_engagement_as_all_pending(t *testing.T) {
	// Given
	eng := newEngagement(t, domain.ModeFull)

	// When
	states := stageStates(&eng)

	// Then
	for i, state := range states {
		assert.Equal(t, stageStatePending, state, "stage %d", i)
	}
}

// runningAt returns an engagement running at the given stage in full mode.
func runningAt(t *testing.T, stage domain.StageKind) domain.Engagement {
	t.Helper()
	return runningAtWithMode(t, domain.ModeFull, stage)
}

// runningAtWithMode returns an engagement of the given mode running at a stage.
func runningAtWithMode(t *testing.T, mode domain.Mode, stage domain.StageKind) domain.Engagement {
	t.Helper()

	eng := newEngagement(t, mode)
	ready, err := eng.Authorize(domain.TimestampMs(1735689601000))
	require.NoError(t, err)

	running, err := ready.Start(domain.TimestampMs(1735689602000))
	require.NoError(t, err)

	advanced, err := running.AdvanceTo(stage, domain.TimestampMs(1735689603000))
	require.NoError(t, err)

	return advanced
}

// newEngagement builds a draft engagement in the requested mode.
func newEngagement(t *testing.T, mode domain.Mode) domain.Engagement {
	t.Helper()

	eng, err := domain.NewEngagement(domain.NewEngagementParams{
		Name: "sidebar-test-" + string(mode),
		Mode: mode,
		Authorization: domain.Authorization{
			AuthorizedBy: "tester",
			AuthorizedAt: domain.TimestampMs(1735689600000),
		},
		Targets: []domain.Target{{Kind: domain.TargetDomain, Value: "example.test"}},
		Now:     domain.TimestampMs(1735689600000),
	})
	require.NoError(t, err)
	return eng
}
