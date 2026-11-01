package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_StageRunRepo_Begin_assigns_attempt_one_when_the_stage_has_no_history(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-begin")
	repo := store.NewStageRunRepo(db)
	ids := domain.NewULIDGenerator()

	// When
	run, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, ids.New())

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, run.Attempt)
	assert.Equal(t, domain.StageRunning, run.Status)
	assert.Equal(t, domain.StageRecon, run.Stage)
	assert.Equal(t, engagement.ID, run.EngagementID)
}

func Test_StageRunRepo_Begin_increments_the_attempt_when_a_previous_run_exists(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-attempt")
	repo := store.NewStageRunRepo(db)
	ids := domain.NewULIDGenerator()

	first, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, ids.New())
	require.NoError(t, err)
	first.Status = domain.StageSucceeded
	require.NoError(t, repo.Finish(ctx, first, baseTimestamp+100))

	// When
	second, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp+200, ids.New())

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, second.Attempt, "a re-run must not overwrite the first attempt")
}

func Test_StageRunRepo_Begin_keeps_attempts_independent_when_stages_differ(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-independent")
	repo := store.NewStageRunRepo(db)
	ids := domain.NewULIDGenerator()

	_, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, ids.New())
	require.NoError(t, err)

	// When
	osint, err := repo.Begin(ctx, engagement.ID, domain.StageOSINT, baseTimestamp+1, ids.New())

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, osint.Attempt, "attempt numbering is per stage, not global")
}

func Test_StageRunRepo_Begin_rejects_an_unknown_stage_when_called(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-reject")
	repo := store.NewStageRunRepo(db)

	// When
	_, err := repo.Begin(ctx, engagement.ID, domain.StageKind("bogus"), baseTimestamp, domain.NewULIDGenerator().New())

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrInvalidStageRun)
}

func Test_StageRunRepo_Finish_persists_rollups_when_the_run_is_terminal(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-finish")
	repo := store.NewStageRunRepo(db)

	run, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, domain.NewULIDGenerator().New())
	require.NoError(t, err)

	run.Status = domain.StageSucceeded
	run.Summary = "found three live hosts"
	run.ToolRunCount = 4
	run.FindingCount = 1
	run.FactCount = 12
	run.TokensIn = 900
	run.TokensOut = 120
	run.CostUSDMicros = 3300
	run.Iterations = 7

	// When
	require.NoError(t, repo.Finish(ctx, run, baseTimestamp+5000))

	// Then
	got, err := repo.Latest(ctx, engagement.ID, domain.StageRecon)
	require.NoError(t, err)

	assert.Equal(t, domain.StageSucceeded, got.Status)
	assert.Equal(t, "found three live hosts", got.Summary)
	assert.Equal(t, 4, got.ToolRunCount)
	assert.Equal(t, 1, got.FindingCount)
	assert.Equal(t, 12, got.FactCount)
	assert.Equal(t, 900, got.TokensIn)
	assert.Equal(t, 120, got.TokensOut)
	assert.Equal(t, int64(3300), got.CostUSDMicros)
	assert.Equal(t, 7, got.Iterations)
	assert.Equal(t, baseTimestamp+5000, got.FinishedAt)
}

func Test_StageRunRepo_Finish_rejects_a_non_terminal_status_when_called(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-nonterminal")
	repo := store.NewStageRunRepo(db)

	run, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, domain.NewULIDGenerator().New())
	require.NoError(t, err)
	run.Status = domain.StageRunning

	// When
	err = repo.Finish(ctx, run, baseTimestamp+1)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrInvalidStageRun)
}

func Test_StageRunRepo_Latest_returns_ErrNoStageRun_when_the_stage_never_ran(t *testing.T) {
	// Given
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-missing")
	repo := store.NewStageRunRepo(db)

	// When
	_, err := repo.Latest(context.Background(), engagement.ID, domain.StageExploit)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrNoStageRun)
}

func Test_StageRunRepo_List_returns_attempts_in_start_order_when_several_exist(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-list")
	repo := store.NewStageRunRepo(db)
	ids := domain.NewULIDGenerator()

	for range 3 {
		run, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, ids.New())
		require.NoError(t, err)
		run.Status = domain.StageFailed
		require.NoError(t, repo.Finish(ctx, run, baseTimestamp+100))
	}

	// When
	runs, err := repo.List(ctx, engagement.ID)

	// Then
	require.NoError(t, err)
	require.Len(t, runs, 3)
	for i, run := range runs {
		assert.Equal(t, i+1, run.Attempt, "attempts must come back in order")
	}
}

func Test_StageRunRepo_MarkInterrupted_closes_runs_left_running_by_a_crash(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "stage-crash")
	repo := store.NewStageRunRepo(db)
	ids := domain.NewULIDGenerator()

	_, err := repo.Begin(ctx, engagement.ID, domain.StageRecon, baseTimestamp, ids.New())
	require.NoError(t, err)
	_, err = repo.Begin(ctx, engagement.ID, domain.StageOSINT, baseTimestamp, ids.New())
	require.NoError(t, err)

	// When
	affected, err := repo.MarkInterrupted(ctx, baseTimestamp+10_000, "interrupted by restart")

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, affected)

	got, err := repo.Latest(ctx, engagement.ID, domain.StageRecon)
	require.NoError(t, err)
	assert.Equal(t, domain.StageFailed, got.Status)
	assert.Equal(t, "interrupted by restart", got.Err)
}

func Test_StageRunRepo_MarkInterrupted_returns_zero_when_nothing_is_running(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	repo := store.NewStageRunRepo(db)

	// When
	affected, err := repo.MarkInterrupted(ctx, baseTimestamp, "restart")

	// Then
	require.NoError(t, err)
	assert.Equal(t, 0, affected)
}
