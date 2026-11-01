package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
	"github.com/cosmics/cosmics/internal/store"
)

// seedEngagement persists one engagement and returns it with its ID assigned.
func seedEngagement(t *testing.T, db *store.DB, name string) domain.Engagement {
	t.Helper()

	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, name, baseTimestamp)
	require.NoError(t, store.NewEngagementRepo(db).Create(context.Background(), engagement, targets))
	return engagement
}

// callFixture returns a valid call record for the given engagement and stage.
func callFixture(t *testing.T, engagementID domain.ID, stage domain.StageKind) llm.CallRecord {
	t.Helper()

	return llm.CallRecord{
		ID:             domain.NewULIDGenerator().New(),
		EngagementID:   engagementID,
		Stage:          stage,
		Provider:       llm.KindOpenAICompatible,
		ModelRequested: "astral-projection",
		ModelServed:    "deepseek-v4.1-flash",
		Purpose:        llm.PurposeToolSelect,
		Status:         "ok",
		StartedAt:      baseTimestamp,
		FinishedAt:     baseTimestamp + 1,
	}
}

func Test_LLMCallRepo_Record_persists_every_column_when_the_record_is_valid(t *testing.T) {
	// Given
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "llm-call-persist")

	rec := callFixture(t, engagement.ID, domain.StageRecon)
	rec.Usage = llm.Usage{
		InputTokens:     120,
		OutputTokens:    30,
		CachedTokens:    64,
		ReasoningTokens: 9,
	}
	rec.CostUSDMicros = 4200
	rec.Latency = 1500
	rec.Streamed = true

	// When
	err := store.NewLLMCallRepo(db).Record(context.Background(), rec, `{"model":"astral-projection"}`, `{"ok":true}`)

	// Then
	require.NoError(t, err)

	var (
		served    string
		tokensIn  int
		tokensOut int
		cached    int
		reasoning int
		cost      int64
		streamed  int
	)
	row := db.Reader().QueryRowContext(context.Background(),
		`SELECT model_served, tokens_in, tokens_out, tokens_cached, reasoning_tokens, cost_usd_micros, streamed
		 FROM llm_calls WHERE id = ?`, rec.ID.String())
	require.NoError(t, row.Scan(&served, &tokensIn, &tokensOut, &cached, &reasoning, &cost, &streamed))

	assert.Equal(t, "deepseek-v4.1-flash", served, "the served model must be stored, not the requested one")
	assert.Equal(t, 120, tokensIn)
	assert.Equal(t, 30, tokensOut)
	assert.Equal(t, 64, cached)
	assert.Equal(t, 9, reasoning)
	assert.Equal(t, int64(4200), cost)
	assert.Equal(t, 1, streamed)
}

func Test_LLMCallRepo_Record_rejects_a_record_the_schema_would_refuse(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*llm.CallRecord)
	}{
		{"zero id", func(r *llm.CallRecord) { r.ID = domain.ID{} }},
		{"zero engagement", func(r *llm.CallRecord) { r.EngagementID = domain.ID{} }},
		{"unknown stage", func(r *llm.CallRecord) { r.Stage = domain.StageKind("bogus") }},
		{"unknown provider", func(r *llm.CallRecord) { r.Provider = llm.ProviderKind("bogus") }},
		{"unknown purpose", func(r *llm.CallRecord) { r.Purpose = llm.Purpose("bogus") }},
		{"unknown status", func(r *llm.CallRecord) { r.Status = "bogus" }},
		{"negative tokens", func(r *llm.CallRecord) { r.Usage.InputTokens = -1 }},
		{"finished before started", func(r *llm.CallRecord) { r.FinishedAt = r.StartedAt - 1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			db := openTestDB(t)
			engagement := seedEngagement(t, db, "llm-call-reject")

			rec := callFixture(t, engagement.ID, domain.StageRecon)
			tt.mutate(&rec)

			// When
			err := store.NewLLMCallRepo(db).Record(context.Background(), rec, "{}", "{}")

			// Then
			require.Error(t, err)
			assert.ErrorIs(t, err, store.ErrInvalidCallRecord)
		})
	}
}

func Test_LLMCallRepo_SumByStage_totals_only_the_requested_stage_when_several_exist(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "llm-call-sum")
	repo := store.NewLLMCallRepo(db)

	write := func(stage domain.StageKind, in, out int, cost int64) {
		rec := callFixture(t, engagement.ID, stage)
		rec.Usage = llm.Usage{InputTokens: in, OutputTokens: out}
		rec.CostUSDMicros = cost
		require.NoError(t, repo.Record(ctx, rec, "{}", "{}"))
	}
	write(domain.StageRecon, 10, 1, 100)
	write(domain.StageRecon, 20, 2, 200)
	write(domain.StageOSINT, 30, 3, 300)

	// When
	gotIn, gotOut, gotCost, err := repo.SumByStage(ctx, engagement.ID, domain.StageRecon)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 30, gotIn)
	assert.Equal(t, 3, gotOut, "recon contributed 1 + 2; the osint row's 3 must be excluded")
	assert.Equal(t, int64(300), gotCost)
}

func Test_LLMCallRepo_CountByEngagement_counts_every_call_when_none_are_filtered(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "llm-call-count")
	repo := store.NewLLMCallRepo(db)

	require.NoError(t, repo.Record(ctx, callFixture(t, engagement.ID, domain.StageRecon), "{}", "{}"))
	require.NoError(t, repo.Record(ctx, callFixture(t, engagement.ID, domain.StageOSINT), "{}", "{}"))

	// When
	got, err := repo.CountByEngagement(ctx, engagement.ID)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 2, got)
}

func Test_LLMCallRepo_Record_is_rejected_by_the_database_when_the_row_is_updated(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "llm-call-immutable")

	rec := callFixture(t, engagement.ID, domain.StageRecon)
	require.NoError(t, store.NewLLMCallRepo(db).Record(ctx, rec, "{}", "{}"))

	// When
	_, err := db.Writer().ExecContext(ctx,
		`UPDATE llm_calls SET tokens_in = 999 WHERE id = ?`, rec.ID.String())

	// Then
	require.Error(t, err, "the append-only trigger must reject an update")
	assert.Contains(t, err.Error(), "append-only")
}
