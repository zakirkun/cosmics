package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

const approvalRefCreatedAt = domain.TimestampMs(1_760_000_000_000)

// approvalFixture builds a valid pending approval for a live engagement.
func approvalFixture(t *testing.T, db *store.DB) store.ApprovalRow {
	t.Helper()

	createdAt := approvalRefCreatedAt
	engagement, targets := draftFixture(t, domain.NewULIDGenerator(), "approval-test-"+domain.NewULIDGenerator().New().String(), createdAt)
	require.NoError(t, store.NewEngagementRepo(db).Create(t.Context(), engagement, targets))

	return store.ApprovalRow{
		ID:             domain.NewULIDGenerator().New(),
		EngagementID:   engagement.ID,
		Kind:           "exploit_launch",
		CommandPreview: "sqlmap -u https://portal.acme.test --batch --level=1",
		Reason:         "confirm the injection suspected in the login form",
		Decision:       "pending",
		ExpiresAt:      createdAt + 300_000,
		CreatedAt:      createdAt,
	}
}

func Test_ApprovalRepo_Create_forces_pending_regardless_of_the_supplied_decision(t *testing.T) {
	// Given
	db := openTestDB(t)
	row := approvalFixture(t, db)
	row.Decision = "approved"

	// When
	stored, err := store.NewApprovalRepo(db).Create(t.Context(), row)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "pending", stored.Decision,
		"a newly created approval is never pre-approved")
	assert.Equal(t, domain.TimestampMs(0), stored.DecidedAt)
	assert.Empty(t, stored.DecidedBy)
}

func Test_ApprovalRepo_Create_rejects_an_empty_command_preview_when_missing(t *testing.T) {
	// Given
	db := openTestDB(t)
	row := approvalFixture(t, db)
	row.CommandPreview = ""

	// When
	_, err := store.NewApprovalRepo(db).Create(t.Context(), row)

	// Then
	require.ErrorIs(t, err, store.ErrInvalidApproval)
	assert.Contains(t, err.Error(), "command_preview")
}

func Test_ApprovalRepo_Create_rejects_an_unknown_kind_when_given_one(t *testing.T) {
	// Given
	db := openTestDB(t)
	row := approvalFixture(t, db)
	row.Kind = "mind_control"

	// When
	_, err := store.NewApprovalRepo(db).Create(t.Context(), row)

	// Then
	require.ErrorIs(t, err, store.ErrInvalidApproval)
	assert.Contains(t, err.Error(), "kind")
}

func Test_ApprovalRepo_Create_rejects_an_expiry_before_creation_when_malformed(t *testing.T) {
	// Given
	db := openTestDB(t)
	row := approvalFixture(t, db)
	row.ExpiresAt = row.CreatedAt - 1

	// When
	_, err := store.NewApprovalRepo(db).Create(t.Context(), row)

	// Then
	require.ErrorIs(t, err, store.ErrInvalidApproval)
	assert.Contains(t, err.Error(), "expires_at")
}

func Test_ApprovalRepo_Get_returns_ErrNotFound_when_the_id_is_absent(t *testing.T) {
	// Given
	db := openTestDB(t)

	// When
	_, err := store.NewApprovalRepo(db).Get(t.Context(), domain.NewULIDGenerator().New())

	// Then
	require.ErrorIs(t, err, store.ErrNotFound)
}

func Test_ApprovalRepo_Decide_records_the_decision_when_the_row_is_pending(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	// When
	decided, err := repo.Decide(t.Context(), stored.ID, "approved", "dana.reyes", row.CreatedAt+1_000)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "approved", decided.Decision)
	assert.Equal(t, "dana.reyes", decided.DecidedBy)
	assert.Equal(t, row.CreatedAt+1_000, decided.DecidedAt)
}

func Test_ApprovalRepo_Decide_refuses_a_second_decision_when_already_terminal(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	_, err = repo.Decide(t.Context(), stored.ID, "denied", "dana.reyes", row.CreatedAt+1_000)
	require.NoError(t, err)

	// When
	_, err = repo.Decide(t.Context(), stored.ID, "approved", "someone.else", row.CreatedAt+2_000)

	// Then
	require.ErrorIs(t, err, store.ErrApprovalAlreadyDecided,
		"a decision is final; the first answer wins")
}

func Test_ApprovalRepo_Decide_marks_a_lapsed_request_expired_rather_than_approving_it(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	// When: the operator answers after the window closed.
	decided, err := repo.Decide(t.Context(), stored.ID, "approved", "dana.reyes", row.ExpiresAt+1)

	// Then
	require.ErrorIs(t, err, store.ErrApprovalExpired,
		"an approval past its deadline must not become usable")

	after, getErr := repo.Get(t.Context(), stored.ID)
	require.NoError(t, getErr)
	assert.Equal(t, "expired", after.Decision)
	assert.Equal(t, "expired", decided.Decision)
}

func Test_ApprovalRepo_Decide_rejects_pending_as_a_decision_when_given_one(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	// When
	_, err = repo.Decide(t.Context(), stored.ID, "pending", "dana.reyes", row.CreatedAt+1_000)

	// Then
	require.ErrorIs(t, err, store.ErrInvalidApproval)
}

func Test_ApprovalRepo_ExpirePending_flips_only_overdue_pending_rows_when_run(t *testing.T) {
	// Given: one approval already past its deadline and one still live, both
	// belonging to the same engagement so a single sweep can see them.
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)

	overdue := approvalFixture(t, db)
	overdueRow, err := repo.Create(t.Context(), overdue)
	require.NoError(t, err)

	live := approvalFixture(t, db)
	live.EngagementID = overdueRow.EngagementID
	live.ExpiresAt = live.CreatedAt + 10_000_000
	liveRow, err := repo.Create(t.Context(), live)
	require.NoError(t, err)

	// When: the sweep runs after the first window closed but before the second.
	expired, err := repo.ExpirePending(t.Context(), overdueRow.EngagementID, overdueRow.ExpiresAt+1)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, expired, "exactly the overdue row must be swept")

	overdueAfter, err := repo.Get(t.Context(), overdueRow.ID)
	require.NoError(t, err)
	assert.Equal(t, "expired", overdueAfter.Decision,
		"a pending row past its window is spent")

	liveAfter, err := repo.Get(t.Context(), liveRow.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", liveAfter.Decision,
		"a live pending row must survive the sweep")
}

func Test_ApprovalRepo_ListPending_returns_only_pending_rows_when_some_are_decided(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)

	first := approvalFixture(t, db)
	firstRow, err := repo.Create(t.Context(), first)
	require.NoError(t, err)

	second := approvalFixture(t, db)
	secondRow, err := repo.Create(t.Context(), second)
	require.NoError(t, err)

	_, err = repo.Decide(t.Context(), firstRow.ID, "denied", "dana.reyes", first.CreatedAt+1_000)
	require.NoError(t, err)

	// When
	pending, err := repo.ListPending(t.Context(), secondRow.EngagementID)

	// Then
	require.NoError(t, err)

	for _, row := range pending {
		assert.Equal(t, "pending", row.Decision)
		assert.NotEqual(t, firstRow.ID, row.ID, "a decided row must not be listed as pending")
	}
}

func Test_ApprovalRepo_IsApprovalKind_matches_the_check_list_when_compared(t *testing.T) {
	want := []string{"destructive_action", "exploit_launch", "credential_use", "data_access"}

	for _, kind := range want {
		assert.True(t, store.IsApprovalKind(kind), "kind %q must be accepted", kind)
	}
	assert.False(t, store.IsApprovalKind("other"))
	assert.False(t, store.IsApprovalKind(""))
}

func Test_ApprovalRepo_IsApprovalDecision_matches_the_check_list_when_compared(t *testing.T) {
	want := []string{"pending", "approved", "denied", "expired"}

	for _, decision := range want {
		assert.True(t, store.IsApprovalDecision(decision), "decision %q must be accepted", decision)
	}
	assert.False(t, store.IsApprovalDecision("maybe"))
}

func Test_ApprovalRepo_Create_and_Get_roundtrip_every_field_when_compared(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	// When
	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	got, err := repo.Get(t.Context(), stored.ID)

	// Then
	require.NoError(t, err)
	assert.Equal(t, row.ID, got.ID)
	assert.Equal(t, row.EngagementID, got.EngagementID)
	assert.Equal(t, row.Kind, got.Kind)
	assert.Equal(t, row.CommandPreview, got.CommandPreview)
	assert.Equal(t, row.Reason, got.Reason)
	assert.Equal(t, row.ExpiresAt, got.ExpiresAt)
	assert.Equal(t, row.CreatedAt, got.CreatedAt)
}

// Ensure the context import is exercised by a cancellation test.
func Test_ApprovalRepo_Get_returns_an_error_when_the_context_is_cancelled(t *testing.T) {
	// Given
	db := openTestDB(t)
	repo := store.NewApprovalRepo(db)
	row := approvalFixture(t, db)

	stored, err := repo.Create(t.Context(), row)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err = repo.Get(ctx, stored.ID)

	// Then
	require.Error(t, err)
}
