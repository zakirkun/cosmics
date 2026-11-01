package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_EngagementCreate_round_trips_every_persisted_field(t *testing.T) {
	// Given: a fully populated draft engagement and one target.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	want, targets := draftFixture(t, ids, "Acme external perimeter", baseTimestamp)

	// When: it is created and read back by ID.
	require.NoError(t, repo.Create(t.Context(), want, targets))
	got, err := repo.Get(t.Context(), want.ID)
	require.NoError(t, err)

	// Then: every mapped field survives the round trip.
	assert.Equal(t, want.ID.String(), got.ID.String())
	assert.Equal(t, "Acme external perimeter", got.Name)
	assert.Equal(t, "Acme Corporation", got.Client)
	assert.Equal(t, domain.ModeFull, got.Mode)
	assert.Equal(t, domain.StatusDraft, got.Status)
	assert.Equal(t, domain.EngageStageNone, got.Stage)
	assert.Equal(t, "reasoning-tier", got.Profile)

	assert.Equal(t, "dana.reyes", got.Authorization.AuthorizedBy)
	assert.Equal(t, "SOW-2026-114 signed engagement letter", got.Authorization.Note)
	assert.Equal(t, want.Authorization.AuthorizedAt, got.Authorization.AuthorizedAt)

	assert.Equal(t, int64(25_000_000), got.BudgetUSDMicros)
	assert.Equal(t, int64(120_000), got.SpentUSDMicros)
	assert.Equal(t, int64(4_000_000), got.TokenBudget)
	assert.Equal(t, int64(9_500), got.TokensUsed)

	assert.Equal(t, baseTimestamp, got.CreatedAt)
	assert.Equal(t, baseTimestamp, got.UpdatedAt)
	assert.True(t, got.StartedAt.IsZero(), "a draft has not started")
	assert.True(t, got.FinishedAt.IsZero(), "a draft has not finished")

	assert.Equal(t, `{"tool_timeout_s":600,"models":["a","b"]}`, got.ConfigJSON)
	assert.Empty(t, got.Err)
}

func Test_EngagementCreate_persists_the_seed_target(t *testing.T) {
	// Given: a draft engagement with one domain target.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Target persistence", baseTimestamp)

	// When: it is created.
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// Then: the target row carries the normalized value, the raw input, and the
	// engagement's ID - not whatever the caller left in Target.EngagementID.
	var (
		engagementID string
		kind         string
		value        string
		raw          string
		note         string
		createdAt    int64
	)
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT engagement_id, kind, value, raw, note, created_at FROM engagement_targets`,
	).Scan(&engagementID, &kind, &value, &raw, &note, &createdAt))

	assert.Equal(t, engagement.ID.String(), engagementID)
	assert.Equal(t, string(domain.TargetDomain), kind)
	assert.Equal(t, "portal.acme.test", value)
	assert.Equal(t, "  portal.acme.test  ", raw)
	assert.Equal(t, "primary external web surface", note)
	assert.Equal(t, int64(baseTimestamp), createdAt)
}

func Test_EngagementCreate_writes_the_two_creation_audit_entries(t *testing.T) {
	// Given: a draft engagement.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Audited creation", baseTimestamp)

	// When: it is created.
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// Then: exactly one engagement_created and one authorization_recorded row
	// exist, both attributed to the operator and to the engagement.
	audit := store.NewAuditRepo(db)
	entries, err := audit.ListByEngagement(t.Context(), engagement.ID)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	actions := make(map[string]int, len(entries))
	for _, entry := range entries {
		actions[entry.Action]++
		assert.Equal(t, engagement.ID.String(), entry.EngagementID)
		assert.Equal(t, string(domain.ActorOperator), entry.Actor)
		assert.Equal(t, string(domain.OutcomeAllowed), entry.Outcome)
		assert.NotEmpty(t, entry.EntryHash)
	}
	assert.Equal(t, map[string]int{
		string(domain.AuditEngagementCreated):     1,
		string(domain.AuditAuthorizationRecorded): 1,
	}, actions)

	// Then: the authorization entry names the actual authorizing party.
	for _, entry := range entries {
		if entry.Action == string(domain.AuditAuthorizationRecorded) {
			assert.Equal(t, "dana.reyes", entry.Target)
			assert.Contains(t, entry.DetailJSON, "dana.reyes")
			assert.Contains(t, entry.DetailJSON, "SOW-2026-114")
		}
	}
}

func Test_EngagementGet_returns_ErrNotFound_when_the_id_is_absent(t *testing.T) {
	// Given: an empty database.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	missingID, err := domain.NewID("01J00000000000000000000000")
	require.NoError(t, err)

	// When: the engagement is looked up.
	_, err = repo.Get(t.Context(), missingID)

	// Then: the failure is the typed not-found sentinel.
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func Test_EngagementGetByName_finds_the_engagement(t *testing.T) {
	// Given: two created engagements with distinct names.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	first, firstTargets := draftFixture(t, ids, "Alpha perimeter", baseTimestamp)
	second, secondTargets := draftFixture(t, ids, "Beta perimeter", baseTimestamp+1_000)
	require.NoError(t, repo.Create(t.Context(), first, firstTargets))
	require.NoError(t, repo.Create(t.Context(), second, secondTargets))

	// When: one is fetched by name.
	got, err := repo.GetByName(t.Context(), "Beta perimeter")
	require.NoError(t, err)

	// Then: the matching row comes back, not the other one.
	assert.Equal(t, second.ID.String(), got.ID.String())
	assert.Equal(t, "Beta perimeter", got.Name)
}

func Test_EngagementGetByName_returns_ErrNotFound_when_the_name_is_absent(t *testing.T) {
	// Given: one engagement named "Alpha perimeter".
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Alpha perimeter", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// When: an unrelated name is queried.
	_, err := repo.GetByName(t.Context(), "No such engagement")

	// Then: it is reported as not found.
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func Test_EngagementList_returns_newest_first(t *testing.T) {
	// Given: three engagements created one second apart, in ascending order.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()

	for i, name := range []string{"Oldest", "Middle", "Newest"} {
		createdAt := baseTimestamp + domain.TimestampMs(i)*1_000
		engagement, targets := draftFixture(t, ids, name, createdAt)
		require.NoError(t, repo.Create(t.Context(), engagement, targets))
	}

	// When: all engagements are listed.
	list, err := repo.List(t.Context())
	require.NoError(t, err)

	// Then: the newest is first and the order is monotonically descending.
	require.Len(t, list, 3)
	assert.Equal(t, []string{"Newest", "Middle", "Oldest"},
		[]string{list[0].Name, list[1].Name, list[2].Name})

	for i := 1; i < len(list); i++ {
		assert.GreaterOrEqual(t, int64(list[i-1].CreatedAt), int64(list[i].CreatedAt),
			"created_at must not increase down the list")
	}
}

func Test_EngagementList_is_empty_when_no_engagements_exist(t *testing.T) {
	// Given: an empty database.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)

	// When: the list is requested.
	list, err := repo.List(t.Context())

	// Then: it is empty, and not an error.
	require.NoError(t, err)
	assert.Empty(t, list)
}

func Test_EngagementCreate_rejects_a_duplicate_name_when_the_unique_index_is_hit(t *testing.T) {
	// Given: one engagement already stored under a name.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Duplicate name", baseTimestamp)
	require.NoError(t, repo.Create(t.Context(), engagement, targets))

	// When: a second engagement reuses that name.
	duplicate, duplicateTargets := draftFixture(t, ids, "Duplicate name", baseTimestamp+1_000)
	err := repo.Create(t.Context(), duplicate, duplicateTargets)

	// Then: the constraint rejects it and the failed transaction left no rows.
	require.Error(t, err)
	assertCount(t, db, `SELECT COUNT(*) FROM engagements`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM audit_trail`, 2)
}

func Test_EngagementCreate_rejects_an_engagement_without_targets(t *testing.T) {
	// Given: a valid engagement and an empty target slice.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, _ := draftFixture(t, ids, "No targets", baseTimestamp)

	// When: it is created with no targets.
	err := repo.Create(t.Context(), engagement, nil)

	// Then: it is rejected with the domain's sentinel.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNoTargets)
}

func Test_EngagementCreate_rejects_an_engagement_without_authorization(t *testing.T) {
	// Given: an engagement whose authorization was stripped after construction.
	db := openTestDB(t)
	repo := store.NewEngagementRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Unauthorized", baseTimestamp)
	engagement.Authorization = domain.Authorization{}

	// When: it is created.
	err := repo.Create(t.Context(), engagement, targets)

	// Then: the store refuses before SQL and writes nothing.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
	assertCount(t, db, `SELECT COUNT(*) FROM audit_trail`, 0)
}

// assertCount fails unless the scalar query returns want.
func assertCount(t *testing.T, db *store.DB, query string, want int) {
	t.Helper()

	var got int
	require.NoError(t, db.Reader().QueryRowContext(context.Background(), query).Scan(&got))
	assert.Equal(t, want, got, query)
}
