package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Engagement_Lifecycle_ReachesCompleted_when_FullModeRunsEveryStage(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.Mode = domain.ModeFull

	e, err := domain.NewEngagement(p)
	require.NoError(t, err)
	require.Equal(t, domain.StatusDraft, e.Status)
	require.Equal(t, domain.EngageStageNone, e.Stage)

	// When: authorize
	e, err = e.Authorize(t1)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusReady, e.Status)
	require.Equal(t, domain.EngageStageNone, e.Stage)
	require.Equal(t, t1, e.UpdatedAt)
	require.True(t, e.StartedAt.IsZero())

	// When: start
	e, err = e.Start(t2)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusRunning, e.Status)
	require.Equal(t, domain.EngageStageRecon, e.Stage)
	require.Equal(t, t2, e.StartedAt)
	require.Equal(t, t2, e.UpdatedAt)
	require.True(t, e.FinishedAt.IsZero())

	// When: walk every later stage in mode order
	for _, stage := range p.Mode.Stages()[1:] {
		e, err = e.AdvanceTo(stage, t2)
		require.NoError(t, err)

		// Then
		require.Equal(t, domain.StatusRunning, e.Status)
		require.Equal(t, domain.EngageStageOf(stage), e.Stage)
		require.Equal(t, t2, e.UpdatedAt)
	}

	// When: complete
	e, err = e.Complete(t3)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StatusCompleted, e.Status)
	require.Equal(t, domain.EngageStageDone, e.Stage)
	require.Equal(t, t3, e.FinishedAt)
	require.Equal(t, t3, e.UpdatedAt)
	require.Equal(t, t2, e.StartedAt)
	require.True(t, e.Status.Terminal())
}

func Test_Engagement_Start_ReturnsIllegalMove_when_StatusIsDraft(t *testing.T) {
	// Given
	e := draftEngagement(t)

	// When
	got, err := e.Start(t2)

	// Then
	require.ErrorIs(t, err, domain.ErrIllegalMove)
	require.Equal(t, domain.Engagement{}, got)
	require.Equal(t, domain.StatusDraft, e.Status)
	require.Equal(t, domain.EngageStageNone, e.Stage)
}

func Test_Engagement_Authorize_ReturnsIllegalMove_when_StatusIsNotDraft(t *testing.T) {
	tests := []struct {
		name string
		base domain.Engagement
	}{
		{name: "ready", base: readyEngagement(t)},
		{name: "running", base: runningEngagement(t)},
		{name: "paused", base: func() domain.Engagement {
			e, err := runningEngagement(t).Pause(t3)
			require.NoError(t, err)
			return e
		}()},
		{name: "completed", base: func() domain.Engagement {
			e, err := runningEngagement(t).Complete(t3)
			require.NoError(t, err)
			return e
		}()},
		{name: "failed", base: func() domain.Engagement {
			e, err := runningEngagement(t).Fail(t3, errors.New("boom"))
			require.NoError(t, err)
			return e
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			before := tt.base

			// When
			got, err := before.Authorize(t4)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.True(t, before.Status.Valid())
			assert.NotEqual(t, domain.StatusDraft, before.Status)
		})
	}
}

func Test_Engagement_Authorize_ReturnsIllegalMove_when_CalledTwice(t *testing.T) {
	// Given
	first, err := draftEngagement(t).Authorize(t1)
	require.NoError(t, err)
	require.Equal(t, domain.StatusReady, first.Status)

	// When
	second, err := first.Authorize(t2)

	// Then
	require.ErrorIs(t, err, domain.ErrIllegalMove)
	require.Equal(t, domain.Engagement{}, second)
	require.Equal(t, t1, first.UpdatedAt)
}

func Test_Engagement_Complete_ReturnsIllegalMove_when_StatusIsPaused(t *testing.T) {
	// Given
	paused, err := runningEngagement(t).Pause(t3)
	require.NoError(t, err)
	require.Equal(t, domain.StatusPaused, paused.Status)

	// When
	got, err := paused.Complete(t4)

	// Then
	require.ErrorIs(t, err, domain.ErrIllegalMove)
	require.Equal(t, domain.Engagement{}, got)
	require.Equal(t, domain.StatusPaused, paused.Status)
	require.True(t, paused.FinishedAt.IsZero())
}

func Test_Engagement_Complete_ReturnsIllegalMove_when_StatusIsDraftOrReady(t *testing.T) {
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
			got, err := before.Complete(t3)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.NotEqual(t, domain.StatusCompleted, before.Status)
			assert.True(t, before.FinishedAt.IsZero(), "a rejected completion must not stamp the finish instant")
		})
	}
}

func Test_Engagement_AdvanceTo_ReturnsIllegalMove_when_StageIsExcludedByMode(t *testing.T) {
	tests := []struct {
		name  string
		mode  domain.Mode
		stage domain.StageKind
	}{
		{name: "recon cannot exploit", mode: domain.ModeRecon, stage: domain.StageExploit},
		{name: "recon cannot analyse", mode: domain.ModeRecon, stage: domain.StageAnalysis},
		{name: "recon cannot map", mode: domain.ModeRecon, stage: domain.StageMapper},
		{name: "assess cannot exploit", mode: domain.ModeAssess, stage: domain.StageExploit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEngagementParams(t)
			p.Mode = tt.mode

			e, err := domain.NewEngagement(p)
			require.NoError(t, err)

			e, err = e.Authorize(t1)
			require.NoError(t, err)

			e, err = e.Start(t2)
			require.NoError(t, err)

			before := e.Stage

			// When
			got, err := e.AdvanceTo(tt.stage, t3)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.Equal(t, before, e.Stage)
			assert.Equal(t, t2, e.UpdatedAt)
		})
	}
}

func Test_Engagement_AdvanceTo_ReturnsIllegalMove_when_StatusIsNotRunning(t *testing.T) {
	paused := func() domain.Engagement {
		e, err := runningEngagement(t).Pause(t3)
		require.NoError(t, err)
		return e
	}
	completed := func() domain.Engagement {
		e, err := runningEngagement(t).Complete(t3)
		require.NoError(t, err)
		return e
	}

	tests := []struct {
		name      string
		base      domain.Engagement
		wantStage domain.EngageStage
	}{
		{name: "draft", base: draftEngagement(t), wantStage: domain.EngageStageNone},
		{name: "ready", base: readyEngagement(t), wantStage: domain.EngageStageNone},
		{name: "paused", base: paused(), wantStage: domain.EngageStageRecon},
		{name: "completed", base: completed(), wantStage: domain.EngageStageDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			before := tt.base

			// When
			got, err := before.AdvanceTo(domain.StageOSINT, t4)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.Equal(t, tt.wantStage, before.Stage, "a rejected advance must not move the pointer")
		})
	}
}

func Test_Engagement_AdvanceTo_ReturnsUnknownStage_when_StageIsUndeclared(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.StageKind
	}{
		{name: "unknown", stage: domain.StageKind("bogus")},
		{name: "empty", stage: domain.StageKind("")},
		{name: "pointer value", stage: domain.StageKind("none")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			e := runningEngagement(t)
			before := e.Stage

			// When
			got, err := e.AdvanceTo(tt.stage, t3)

			// Then
			require.ErrorIs(t, err, domain.ErrUnknownStage)
			require.NotErrorIs(t, err, domain.ErrIllegalMove)
			assert.Equal(t, domain.Engagement{}, got)
			assert.Equal(t, before, e.Stage)
		})
	}
}

func Test_Engagement_AdvanceTo_AcceptsAnyIncludedStage_when_StatusIsRunning(t *testing.T) {
	// Given: the guard is membership-based, not order-based, so a jump within the
	// mode is legal even though callers normally advance one stage at a time.
	e := runningEngagement(t)
	require.Equal(t, domain.EngageStageRecon, e.Stage)

	// When
	forward, err := e.AdvanceTo(domain.StageExploit, t3)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.EngageStageExploit, forward.Stage)
	require.Equal(t, t3, forward.UpdatedAt)

	// Given: recon only runs recon, osint and reporting.
	p := validEngagementParams(t)
	p.Mode = domain.ModeRecon

	recon, err := domain.NewEngagement(p)
	require.NoError(t, err)
	recon, err = recon.Authorize(t1)
	require.NoError(t, err)
	recon, err = recon.Start(t2)
	require.NoError(t, err)

	// When
	got, err := recon.AdvanceTo(domain.StageReporting, t3)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.EngageStageReporting, got.Stage)
}
