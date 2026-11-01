package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_EngagementUpdateStatus_moves_a_legal_transition(t *testing.T) {
	// Given: a draft engagement.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Legal transition", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// When: it is authorized (draft -> ready).
	at := baseTimestamp + 5_000
	require.NoError(t, repo.UpdateStatus(t.Context(), engagement.ID,
		domain.StatusDraft, domain.StatusReady, domain.EngageStageRecon, at))

	// Then: the stored row reflects the move and the audit trail records it.
	got, err := repo.Get(t.Context(), engagement.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusReady, got.Status)
	assert.Equal(t, domain.EngageStageRecon, got.Stage)
	assert.Equal(t, at, got.UpdatedAt)

	audit := store.NewAuditRepo(db)
	entries, err := audit.ListByEngagement(t.Context(), engagement.ID)
	require.NoError(t, err)

	var statusChanges int
	for _, entry := range entries {
		if entry.Action == string(domain.AuditEngagementStatusChanged) {
			statusChanges++
			assert.Contains(t, entry.DetailJSON, string(domain.StatusDraft))
			assert.Contains(t, entry.DetailJSON, string(domain.StatusReady))
		}
	}
	assert.Equal(t, 1, statusChanges)
}

func Test_EngagementUpdateStatus_returns_ErrIllegalMove_when_the_from_status_is_stale(t *testing.T) {
	// Given: an engagement that has already moved draft -> ready.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Lost race", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))
	require.NoError(t, repo.UpdateStatus(t.Context(), engagement.ID,
		domain.StatusDraft, domain.StatusReady, domain.EngageStageRecon, baseTimestamp+1_000))

	// When: a second writer that still believes the row is draft tries the same
	// compare-and-set move.
	err := repo.UpdateStatus(t.Context(), engagement.ID,
		domain.StatusDraft, domain.StatusReady, domain.EngageStageRecon, baseTimestamp+2_000)

	// Then: the guard rejects it, and the row keeps the winner's state.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrIllegalMove)
	assert.NotErrorIs(t, err, store.ErrNotFound)

	got, err := repo.Get(t.Context(), engagement.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusReady, got.Status)
	assert.Equal(t, baseTimestamp+1_000, got.UpdatedAt, "the loser must not overwrite updated_at")

	assertCount(t, db,
		`SELECT COUNT(*) FROM audit_trail WHERE action = 'engagement_status_changed'`, 1)
}

func Test_EngagementUpdateStatus_returns_ErrIllegalMove_when_the_transition_is_forbidden(t *testing.T) {
	// Given: a draft engagement.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Forbidden transition", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// When: it is asked to jump straight from draft to running.
	err := repo.UpdateStatus(t.Context(), engagement.ID,
		domain.StatusDraft, domain.StatusRunning, domain.EngageStageRecon, baseTimestamp)

	// Then: the domain matrix rejects it before SQL runs, so nothing changed.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrIllegalMove)

	got, err := repo.Get(t.Context(), engagement.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDraft, got.Status)
}

func Test_EngagementUpdateStatus_returns_ErrNotFound_when_the_engagement_is_absent(t *testing.T) {
	// Given: an empty database.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	missingID, err := domain.NewID("01J00000000000000000000000")
	require.NoError(t, err)

	// When: a legal transition is applied to a row that does not exist.
	err = repo.UpdateStatus(t.Context(), missingID,
		domain.StatusDraft, domain.StatusReady, domain.EngageStageRecon, baseTimestamp)

	// Then: the missing row is distinguishable from a lost race.
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrNotFound)
	assert.NotErrorIs(t, err, domain.ErrIllegalMove)
}

func Test_EngagementUpdateStatus_rejects_an_unknown_stage_when_the_pointer_is_invalid(t *testing.T) {
	// Given: a draft engagement.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Bad stage pointer", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// When: the move names a stage outside the declared set.
	err := repo.UpdateStatus(t.Context(), engagement.ID,
		domain.StatusDraft, domain.StatusReady, domain.EngageStage("lateral"), baseTimestamp)

	// Then: it is rejected and the row is untouched.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnknownStage)

	got, err := repo.Get(t.Context(), engagement.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EngageStageNone, got.Stage)
}
