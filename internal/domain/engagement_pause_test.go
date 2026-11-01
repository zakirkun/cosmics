package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Engagement_Fail_RecordsCause_when_CauseIsProvided(t *testing.T) {
	// Given
	e := runningEngagement(t)
	cause := errors.New("tool container exited 137")

	// When
	got, err := e.Fail(t3, cause)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusRunning, e.Status, "the value receiver must not mutate the original")
	require.Equal(t, domain.StatusFailed, got.Status)
	require.Equal(t, cause.Error(), got.Err)
	require.Equal(t, t3, got.FinishedAt)
	require.Equal(t, t3, got.UpdatedAt)
	require.Equal(t, e.Stage, got.Stage, "failing preserves the pipeline pointer")
	require.True(t, got.Status.Terminal())
}

func Test_Engagement_Fail_LeavesErrorEmpty_when_CauseIsNil(t *testing.T) {
	// Given
	e := runningEngagement(t)

	// When
	got, err := e.Fail(t4, nil)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusFailed, got.Status)
	require.Empty(t, got.Err)
	require.Equal(t, t4, got.FinishedAt)
}

func Test_Engagement_Fail_ReturnsIllegalMove_when_StatusIsDraftOrPaused(t *testing.T) {
	paused := func() domain.Engagement {
		e, err := runningEngagement(t).Pause(t3)
		require.NoError(t, err)
		return e
	}

	tests := []struct {
		name string
		base domain.Engagement
	}{
		{name: "draft", base: draftEngagement(t)},
		{name: "ready", base: readyEngagement(t)},
		{name: "paused", base: paused()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			before := tt.base

			// When
			got, err := before.Fail(t4, errors.New("boom"))

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.False(t, before.Status.Terminal())
			assert.Empty(t, before.Err, "a rejected failure must not record a cause")
		})
	}
}

func Test_Engagement_Pause_PreservesStage_when_StatusIsRunning(t *testing.T) {
	// Given
	e, err := runningEngagement(t).AdvanceTo(domain.StageAnalysis, t3)
	require.NoError(t, err)
	require.Equal(t, domain.EngageStageAnalysis, e.Stage)

	// When
	got, err := e.Pause(t4)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusPaused, got.Status)
	require.Equal(t, domain.EngageStageAnalysis, got.Stage)
	require.Equal(t, t4, got.UpdatedAt)
	require.Equal(t, t2, got.StartedAt)
	require.False(t, got.Status.Terminal())
}

func Test_Engagement_Pause_ReturnsIllegalMove_when_StatusIsNotRunning(t *testing.T) {
	tests := []struct {
		name string
		base domain.Engagement
	}{
		{name: "draft", base: draftEngagement(t)},
		{name: "ready", base: readyEngagement(t)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			before := tt.base

			// When
			got, err := before.Pause(t3)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.NotEqual(t, domain.StatusPaused, before.Status)
		})
	}
}

func Test_Engagement_Resume_ReturnsToRunning_when_StatusIsPaused(t *testing.T) {
	// Given
	paused, err := runningEngagement(t).AdvanceTo(domain.StageMapper, t3)
	require.NoError(t, err)
	paused, err = paused.Pause(t3)
	require.NoError(t, err)
	require.Equal(t, domain.StatusPaused, paused.Status)

	// When
	got, err := paused.Resume(t4)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusRunning, got.Status)
	require.Equal(t, domain.EngageStageMapper, got.Stage)
	require.Equal(t, t4, got.UpdatedAt)
	require.Equal(t, paused.StartedAt, got.StartedAt)
}

func Test_Engagement_Resume_ReturnsIllegalMove_when_StatusIsNotPaused(t *testing.T) {
	tests := []struct {
		name string
		base domain.Engagement
	}{
		{name: "draft", base: draftEngagement(t)},
		{name: "running", base: runningEngagement(t)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			before := tt.base

			// When
			got, err := before.Resume(t3)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
		})
	}
}

func Test_Engagement_Start_PreservesStartAndStage_when_ResumingPaused(t *testing.T) {
	// Given
	paused, err := runningEngagement(t).AdvanceTo(domain.StageOSINT, t3)
	require.NoError(t, err)
	paused, err = paused.Pause(t3)
	require.NoError(t, err)
	originalStart := paused.StartedAt

	// When
	got, err := paused.Start(t4)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusRunning, got.Status)
	require.Equal(t, domain.EngageStageOSINT, got.Stage)
	require.Equal(t, originalStart, got.StartedAt, "the first start instant is retained")
	require.Equal(t, t4, got.UpdatedAt)
}

func Test_Engagement_Start_BeginsAtFirstModeStage_when_ModeDiffers(t *testing.T) {
	tests := []struct {
		name string
		mode domain.Mode
		want domain.EngageStage
	}{
		{name: "recon", mode: domain.ModeRecon, want: domain.EngageStageRecon},
		{name: "assess", mode: domain.ModeAssess, want: domain.EngageStageRecon},
		{name: "exploit", mode: domain.ModeExploit, want: domain.EngageStageRecon},
		{name: "full", mode: domain.ModeFull, want: domain.EngageStageRecon},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEngagementParams(t)
			p.Mode = tt.mode

			e, err := domain.NewEngagement(p)
			require.NoError(t, err)
			require.Equal(t, domain.StageRecon, p.Mode.Stages()[0])

			e, err = e.Authorize(t1)
			require.NoError(t, err)

			// When
			got, err := e.Start(t2)

			// Then
			require.NoError(t, err)
			require.Equal(t, domain.StatusRunning, got.Status)
			require.Equal(t, tt.want, got.Stage)
			require.Equal(t, t2, got.StartedAt)
		})
	}
}
