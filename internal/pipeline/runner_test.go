package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

func Test_Run_executes_every_mode_stage_in_canonical_order_when_all_pass(t *testing.T) {
	// Given
	store := newFakeStore()

	var stages []pipeline.Stage
	for _, kind := range domain.AllStages {
		stages = append(stages, passingStage(kind))
	}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: stages,
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	finished, err := runner.Run(context.Background(), engagementFixture(t, domain.ModeFull))

	// Then
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, finished.Status)
	assert.Equal(t, domain.EngageStageDone, finished.Stage)

	assert.Equal(t, []domain.StageKind{
		domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
		domain.StageMapper, domain.StageExploit, domain.StageReporting,
	}, store.begunStages())

	require.Len(t, store.terminal, 1)
	assert.Equal(t, domain.StatusCompleted, store.terminal[0])
}

func Test_Run_skips_stages_the_mode_excludes_when_the_mode_is_recon(t *testing.T) {
	// Given
	store := newFakeStore()

	var stages []pipeline.Stage
	for _, kind := range domain.AllStages {
		stages = append(stages, passingStage(kind))
	}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: stages,
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.StageKind{
		domain.StageRecon, domain.StageOSINT, domain.StageReporting,
	}, store.begunStages(), "recon mode never analyses, maps, or exploits")
}

func Test_Run_stops_when_a_stage_exit_predicate_is_unmet(t *testing.T) {
	// Given
	store := newFakeStore()

	recon := passingStage(domain.StageRecon)
	osint := &stubStage{kind: domain.StageOSINT, exit: pipeline.Unmet("no assets recorded")}
	reporting := passingStage(domain.StageReporting)

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{recon, osint, reporting},
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	finished, err := runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrExitCriteriaUnmet)
	assert.Contains(t, err.Error(), "no assets recorded", "the reason must name what is missing")

	assert.Equal(t, []domain.StageKind{domain.StageRecon, domain.StageOSINT}, store.begunStages(),
		"the run must not continue past an unmet predicate")
	assert.False(t, reporting.entered)

	assert.Equal(t, domain.StatusFailed, finished.Status)
	require.NotEmpty(t, store.finished)
	assert.Equal(t, domain.StageFailed, store.finished[len(store.finished)-1].Status)
}

func Test_Run_reports_a_missing_stage_implementation_when_one_is_absent(t *testing.T) {
	// Given
	store := newFakeStore()
	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{passingStage(domain.StageRecon)},
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeFull))

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrStageMissing)
	assert.Contains(t, err.Error(), "osint")
}

func Test_Run_fails_the_stage_when_enter_returns_an_error(t *testing.T) {
	// Given
	store := newFakeStore()
	stage := &stubStage{kind: domain.StageRecon, enterErr: errors.New("no targets"), exit: pipeline.MetWith()}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{stage},
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no targets")
	assert.False(t, stage.bodied, "a failed precondition must not run the body")
}

func Test_Run_fails_the_stage_when_body_returns_an_error(t *testing.T) {
	// Given
	store := newFakeStore()
	stage := &stubStage{kind: domain.StageRecon, bodyErr: errors.New("tool crashed"), exit: pipeline.MetWith()}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{stage},
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool crashed")
	assert.True(t, stage.bodied)
}

func Test_Run_rejects_an_already_terminal_engagement_when_called(t *testing.T) {
	// Given
	store := newFakeStore()
	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{passingStage(domain.StageRecon)},
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	engagement := engagementFixture(t, domain.ModeRecon)
	engagement.Status = domain.StatusCompleted

	// When
	_, err = runner.Run(context.Background(), engagement)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrAlreadyTerminal)
	assert.Empty(t, store.begunStages())
}

func Test_Run_returns_the_context_error_when_cancelled_mid_run(t *testing.T) {
	// Given
	store := newFakeStore()
	ctx, cancel := context.WithCancel(context.Background())

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{
			passingStage(domain.StageRecon),
			passingStage(domain.StageOSINT),
			passingStage(domain.StageReporting),
		},
		Store: store,
		Clock: fixedClock{at: refNow},
	})
	require.NoError(t, err)

	cancel()

	// When
	_, err = runner.Run(ctx, engagementFixture(t, domain.ModeRecon))

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func Test_StageKinds_returns_registered_stages_in_canonical_order_when_called(t *testing.T) {
	// Given
	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{
			passingStage(domain.StageReporting),
			passingStage(domain.StageRecon),
		},
		Store: newFakeStore(),
		Clock: fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	kinds := runner.StageKinds()

	// Then
	assert.Equal(t, []domain.StageKind{domain.StageRecon, domain.StageReporting}, kinds,
		"registration order must not leak into execution order")
}

func Test_Run_completes_when_only_recon_stages_are_registered_for_recon_mode(t *testing.T) {
	// Given
	store := newFakeStore()
	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{
			passingStage(domain.StageRecon),
			passingStage(domain.StageOSINT),
			passingStage(domain.StageReporting),
		},
		Store: store,
		Clock: fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	finished, err := runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, finished.Status)
	assert.Positive(t, store.advances, "each passing stage must advance the pointer")
}

func Test_Run_advances_the_pointer_once_per_passing_stage_when_all_pass(t *testing.T) {
	// Given
	store := newFakeStore()
	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{
			passingStage(domain.StageRecon),
			passingStage(domain.StageOSINT),
			passingStage(domain.StageReporting),
		},
		Store: store,
		Clock: fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.NoError(t, err)
	assert.Equal(t, 3, store.advances)
}

func Test_Run_uses_the_injected_clock_when_stamping_stage_runs(t *testing.T) {
	// Given
	store := newFakeStore()
	const later = domain.TimestampMs(1800000000000)

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: []pipeline.Stage{
			passingStage(domain.StageRecon),
			passingStage(domain.StageOSINT),
			passingStage(domain.StageReporting),
		},
		Store: store,
		Clock: fixedClock{at: later},
	})
	require.NoError(t, err)

	// When
	finished, err := runner.Run(context.Background(), engagementFixture(t, domain.ModeRecon))

	// Then
	require.NoError(t, err)
	assert.Equal(t, later, finished.UpdatedAt, "domain code must never read the wall clock")
}

func Test_State_Validate_rejects_a_missing_run_when_called(t *testing.T) {
	// Given
	state := pipeline.State{Clock: fixedClock{at: refNow}}

	// When
	err := state.Validate()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrNoState)
}
