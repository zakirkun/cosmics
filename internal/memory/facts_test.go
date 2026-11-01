package memory_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/memory"
	"github.com/cosmics/cosmics/internal/store"
)

// baseTimestamp is a fixed instant so ordering assertions never read the clock.
const baseTimestamp domain.TimestampMs = 1_760_000_000_000

// openTestDB opens a migrated database under t.TempDir and registers its close.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "cosmics.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

// seedEngagement inserts the minimal engagement row the fact foreign key needs.
func seedEngagement(t *testing.T, db *store.DB, id domain.ID) {
	t.Helper()

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO engagements
		 (id, name, mode, status, stage, authorized_by, authorized_at,
		  created_at, updated_at, config_json)
		 VALUES (?, ?, 'full', 'running', 'recon', 'dana.reyes', ?, ?, ?, '{}')`,
		id.String(), "Fixture "+id.String(), int64(baseTimestamp), int64(baseTimestamp), int64(baseTimestamp))
	require.NoError(t, err)
}

// newEngagementID mints one identifier for a fixture engagement.
func newEngagementID() domain.ID {
	return domain.NewULIDGenerator().NewAt(baseTimestamp)
}

// setParams builds a SetParams with sensible defaults for one key.
func setParams(engagementID domain.ID, value string, confidence float64, at domain.TimestampMs) memory.SetParams {
	return memory.SetParams{
		EngagementID: engagementID,
		Subject:      "www.example.com",
		Predicate:    "web.tech",
		Value:        value,
		ValueType:    memory.ValueTypeText,
		Confidence:   confidence,
		SourceKind:   memory.SourceToolRun,
		SourceID:     "01J00000000000000000000009",
		Stage:        domain.StageRecon,
		Now:          at,
	}
}

func Test_Set_supersedes_the_previous_fact_when_confidence_is_equal_or_higher(t *testing.T) {
	// Given: an engagement with one active fact at confidence 0.6.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	first, err := repo.Set(t.Context(), setParams(engagementID, "nginx 1.24", 0.6, baseTimestamp))
	require.NoError(t, err)

	// When: a second fact for the same key is written at a higher confidence.
	second, err := repo.Set(t.Context(), setParams(engagementID, "nginx 1.25", 0.9, baseTimestamp+1_000))
	require.NoError(t, err)

	// Then: the older row now points at the newer one.
	history, err := repo.History(t.Context(), engagementID, "www.example.com", "web.tech")
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, first.ID.String(), history[0].ID.String())
	assert.Equal(t, second.ID.String(), history[0].SupersededBy.String(),
		"the earlier version must be superseded by the later one")
	assert.True(t, history[1].SupersededBy.IsZero(), "the newest version stays active")
}

func Test_Set_supersedes_the_previous_fact_when_confidence_is_equal(t *testing.T) {
	// Given: an engagement with one active fact at confidence 0.8.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	first, err := repo.Set(t.Context(), setParams(engagementID, "nginx", 0.8, baseTimestamp))
	require.NoError(t, err)

	// When: an equal-confidence fact replaces it.
	second, err := repo.Set(t.Context(), setParams(engagementID, "apache", 0.8, baseTimestamp+1_000))
	require.NoError(t, err)

	// Then: the tie supersedes, because the rule is "higher or equal".
	active, err := repo.Active(t.Context(), engagementID, "www.example.com", "web.tech")
	require.NoError(t, err)
	assert.Equal(t, second.ID.String(), active.ID.String())
	assert.Equal(t, "apache", active.Value)

	history, err := repo.History(t.Context(), engagementID, "www.example.com", "web.tech")
	require.NoError(t, err)
	assert.Equal(t, second.ID.String(), history[0].SupersededBy.String())
	assert.NotEqual(t, first.ID.String(), history[0].SupersededBy.String(),
		"the supersede target is the new row, not the old one")
}

func Test_Set_keeps_the_previous_fact_active_when_confidence_is_lower(t *testing.T) {
	// Given: an engagement whose active fact has confidence 0.9.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	strong, err := repo.Set(t.Context(), setParams(engagementID, "nginx 1.24", 0.9, baseTimestamp))
	require.NoError(t, err)

	// When: a weaker fact is written for the same key.
	weak, err := repo.Set(t.Context(), setParams(engagementID, "nginx 1.24 (maybe)", 0.3, baseTimestamp+1_000))
	require.NoError(t, err)

	// Then: the stronger fact is still the active one and the weak row is stored.
	active, err := repo.Active(t.Context(), engagementID, "www.example.com", "web.tech")
	require.NoError(t, err)
	assert.Equal(t, strong.ID.String(), active.ID.String())
	assert.Equal(t, 0.9, active.Confidence)

	history, err := repo.History(t.Context(), engagementID, "www.example.com", "web.tech")
	require.NoError(t, err)
	require.Len(t, history, 2, "the weaker fact is recorded, not discarded")
	assert.Equal(t, strong.ID.String(), history[0].ID.String())
	assert.True(t, history[0].SupersededBy.IsZero(), "the strong fact was not retired")
	assert.Equal(t, weak.ID.String(), history[1].ID.String())
}

func Test_Active_returns_the_superseding_fact_when_a_newer_one_exists(t *testing.T) {
	// Given: three versions of one key, each stronger than the last.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	for i, value := range []string{"one", "two", "three"} {
		confidence := 0.5 + float64(i)*0.1
		_, err := repo.Set(t.Context(),
			setParams(engagementID, value, confidence, baseTimestamp+domain.TimestampMs(i)*1_000))
		require.NoError(t, err)
	}

	// When: the active fact is read.
	active, err := repo.Active(t.Context(), engagementID, "www.example.com", "web.tech")

	// Then: it is the newest version.
	require.NoError(t, err)
	assert.Equal(t, "three", active.Value)
	assert.True(t, active.SupersededBy.IsZero())
	assert.InDelta(t, 0.7, active.Confidence, 0.0001)
}

func Test_Active_returns_ErrNoActiveFact_when_none_exists(t *testing.T) {
	// Given: an engagement with no facts at all.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	// When: an unknown key is read.
	_, err := repo.Active(t.Context(), engagementID, "absent.example.com", "web.tech")

	// Then: the typed sentinel is returned.
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrNoActiveFact)
}

func Test_History_returns_every_version_oldest_first_when_facts_were_superseded(t *testing.T) {
	// Given: four successive observations of one key.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	want := []string{"v1", "v2", "v3", "v4"}
	for i, value := range want {
		_, err := repo.Set(t.Context(),
			setParams(engagementID, value, 0.5+float64(i)*0.1, baseTimestamp+domain.TimestampMs(i)*1_000))
		require.NoError(t, err)
	}

	// When: the full history is read.
	history, err := repo.History(t.Context(), engagementID, "www.example.com", "web.tech")

	// Then: every version is present and ordered oldest first.
	require.NoError(t, err)
	require.Len(t, history, len(want))

	got := make([]string, 0, len(history))
	for _, fact := range history {
		got = append(got, fact.Value)
	}
	assert.Equal(t, want, got)
	assert.True(t, history[3].SupersededBy.IsZero())
	for i := range 3 {
		assert.Equal(t, history[i+1].ID.String(), history[i].SupersededBy.String(),
			"version %d is superseded by the version after it", i)
	}
}

func Test_Set_writes_rows_in_one_transaction_when_superseding(t *testing.T) {
	// Given: an engagement with one active fact.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	first, err := repo.Set(t.Context(), setParams(engagementID, "old", 0.4, baseTimestamp))
	require.NoError(t, err)

	// When: a stronger fact supersedes it.
	second, err := repo.Set(t.Context(), setParams(engagementID, "new", 0.9, baseTimestamp+1_000))
	require.NoError(t, err)

	// Then: both the supersede UPDATE and the INSERT are visible in the committed
	// state - one row retired, two rows stored.
	var (
		total      int
		superseded int
		active     int
	)
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM facts WHERE engagement_id = ?`, engagementID.String()).Scan(&total))
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE superseded_by IS NULL) FROM facts WHERE engagement_id = ?`,
		engagementID.String()).Scan(&superseded, &active))

	assert.Equal(t, 2, total)
	assert.Equal(t, 1, superseded-active, "exactly one row was retired")
	assert.Equal(t, 1, active, "exactly one row is active")

	var retiredBy string
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT superseded_by FROM facts WHERE id = ?`, first.ID.String()).Scan(&retiredBy))
	assert.Equal(t, second.ID.String(), retiredBy)
}

func Test_Set_rejects_an_out_of_range_confidence_when_validating(t *testing.T) {
	// Given: a structurally valid fact with an impossible confidence.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	params := setParams(engagementID, "nginx", 1.5, baseTimestamp)

	// When: it is written.
	_, err := repo.Set(t.Context(), params)

	// Then: it is rejected before SQL with the typed sentinel.
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrInvalidFact)
}

func Test_Set_rejects_a_blank_subject_when_validating(t *testing.T) {
	// Given: a fact with an empty subject.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	params := setParams(engagementID, "nginx", 0.8, baseTimestamp)
	params.Subject = "   "

	// When: it is written.
	_, err := repo.Set(t.Context(), params)

	// Then: the sentinel names the problem.
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrInvalidFact)
}

func Test_ListActive_returns_only_unsuperseded_facts_when_history_exists(t *testing.T) {
	// Given: one superseded chain and one unrelated active fact.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	repo := memory.NewFactRepo(db)

	_, err := repo.Set(t.Context(), setParams(engagementID, "old", 0.3, baseTimestamp))
	require.NoError(t, err)
	_, err = repo.Set(t.Context(), setParams(engagementID, "new", 0.9, baseTimestamp+500))
	require.NoError(t, err)

	unrelated := memory.SetParams{
		EngagementID: engagementID,
		Subject:      "www.example.com",
		Predicate:    "auth.mechanism",
		Value:        "form login",
		ValueType:    memory.ValueTypeText,
		Confidence:   0.7,
		SourceKind:   memory.SourceLLMInference,
		Stage:        domain.StageAnalysis,
		Now:          baseTimestamp + 1_000,
	}
	_, err = repo.Set(t.Context(), unrelated)
	require.NoError(t, err)

	// When: the active set is listed.
	active, err := repo.ListActive(t.Context(), engagementID)

	// Then: the retired version is excluded and both live keys remain.
	require.NoError(t, err)
	require.Len(t, active, 2)

	assert.Equal(t, "auth.mechanism", active[0].Predicate)
	assert.Equal(t, "web.tech", active[1].Predicate)
	assert.Equal(t, "new", active[1].Value, "the superseding version is the live one")
}

func Test_Active_is_scoped_to_one_engagement_when_two_share_a_key(t *testing.T) {
	// Given: two engagements holding a fact under the same subject and predicate.
	db := openTestDB(t)
	firstEngagement := newEngagementID()
	secondEngagement := newEngagementID()
	seedEngagement(t, db, firstEngagement)
	seedEngagement(t, db, secondEngagement)
	repo := memory.NewFactRepo(db)

	_, err := repo.Set(t.Context(), setParams(firstEngagement, "first", 0.9, baseTimestamp))
	require.NoError(t, err)
	_, err = repo.Set(t.Context(), setParams(secondEngagement, "second", 0.9, baseTimestamp))
	require.NoError(t, err)

	// When: each engagement reads its own key.
	firstActive, err := repo.Active(t.Context(), firstEngagement, "www.example.com", "web.tech")
	require.NoError(t, err)
	secondActive, err := repo.Active(t.Context(), secondEngagement, "www.example.com", "web.tech")
	require.NoError(t, err)

	// Then: neither sees the other's value.
	assert.Equal(t, "first", firstActive.Value)
	assert.Equal(t, "second", secondActive.Value)
}
