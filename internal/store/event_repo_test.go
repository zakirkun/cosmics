package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// eventFixture builds a valid event with a caller-supplied instant. No session
// or tool run is linked by default, because event.session_id and
// event.tool_run_id are enforced foreign keys.
func eventFixture(
	t *testing.T,
	ids *domain.ULIDGenerator,
	engagementID domain.ID,
	kind domain.EventKind,
	level domain.Level,
	at domain.TimestampMs,
) domain.Event {
	t.Helper()

	event, err := domain.NewEvent(domain.NewEventParams{
		EngagementID: engagementID,
		Stage:        domain.StageRecon,
		Kind:         kind,
		Level:        level,
		Message:      "nmap discovered 3 open ports on portal.acme.test",
		DetailJSON:   `{"ports":[443,8443,22]}`,
		Now:          at,
	})
	require.NoError(t, err)

	event.ID = ids.NewAt(at)
	return event
}

// seedSession inserts a sessions row so a test can link an event to a real
// session without dragging in the stage-run machinery.
func seedSession(t *testing.T, db *store.DB, id, engagementID string, at int64) {
	t.Helper()

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO sessions
		 (id, engagement_id, title, model, system_prompt_hash, status, created_at, updated_at)
		 VALUES (?, ?, 'fixture', 'model', 'hash', 'open', ?, ?)`,
		id, engagementID, at, at)
	require.NoError(t, err)
}

func Test_EventAppend_round_trips_every_persisted_field(t *testing.T) {
	// Given: an engagement, a real session, and one event referencing it.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()

	engagement, targets := draftFixture(t, ids, "Event round trip", baseTimestamp)
	require.NoError(t, engagementRepo.Create(t.Context(), engagement, targets))

	sessionID := ids.NewAt(baseTimestamp + 1)
	seedSession(t, db, sessionID.String(), engagement.ID.String(), int64(baseTimestamp))

	event := eventFixture(t, ids, engagement.ID, domain.EventToolEnd, domain.LevelWarn, baseTimestamp+500)
	event.SessionID = sessionID

	// When: it is appended and read back.
	require.NoError(t, events.Append(t.Context(), event))
	got, err := events.ListByEngagement(t.Context(), engagement.ID, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// Then: every column survives the round trip.
	assert.Equal(t, event.ID.String(), got[0].ID.String())
	assert.Equal(t, engagement.ID.String(), got[0].EngagementID.String())
	assert.Equal(t, domain.StageRecon, got[0].Stage)
	assert.Equal(t, sessionID.String(), got[0].SessionID.String())
	assert.True(t, got[0].ToolRunID.IsZero(), "no tool run was linked")
	assert.Equal(t, domain.EventToolEnd, got[0].Kind)
	assert.Equal(t, domain.LevelWarn, got[0].Level)
	assert.Equal(t, event.Message, got[0].Message)
	assert.Equal(t, `{"ports":[443,8443,22]}`, got[0].DetailJSON)
	assert.Equal(t, baseTimestamp+500, got[0].CreatedAt)
}

func Test_EventAppend_rejects_an_event_without_an_id(t *testing.T) {
	// Given: a valid event whose ID was never set.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Event without id", baseTimestamp)
	require.NoError(t, engagementRepo.Create(t.Context(), engagement, targets))

	event := eventFixture(t, ids, engagement.ID, domain.EventNote, domain.LevelInfo, baseTimestamp)
	event.ID = domain.ID{}

	// When: it is appended.
	err := events.Append(t.Context(), event)

	// Then: it is rejected and nothing was written.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrEmptyID)

	list, err := events.ListByEngagement(t.Context(), engagement.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func Test_EventListByEngagement_returns_newest_first(t *testing.T) {
	// Given: three events appended oldest-last, so insertion order and
	// chronological order disagree.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Event ordering", baseTimestamp)
	require.NoError(t, engagementRepo.Create(t.Context(), engagement, targets))

	messages := []string{"oldest", "middle", "newest"}
	for i := len(messages) - 1; i >= 0; i-- {
		event, err := domain.NewEvent(domain.NewEventParams{
			EngagementID: engagement.ID,
			Kind:         domain.EventNote,
			Level:        domain.LevelInfo,
			Message:      messages[i],
			Now:          baseTimestamp + domain.TimestampMs(i)*1_000,
		})
		require.NoError(t, err)
		event.ID = ids.NewAt(baseTimestamp + domain.TimestampMs(i)*1_000)
		require.NoError(t, events.Append(t.Context(), event))
	}

	// When: the feed is read.
	list, err := events.ListByEngagement(t.Context(), engagement.ID, 10)
	require.NoError(t, err)

	// Then: created_at descending wins over insertion order.
	require.Len(t, list, 3)
	assert.Equal(t, []string{"newest", "middle", "oldest"},
		[]string{list[0].Message, list[1].Message, list[2].Message})
}

func Test_EventListByEngagement_honours_the_limit(t *testing.T) {
	// Given: five events for one engagement.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Event limit", baseTimestamp)
	require.NoError(t, engagementRepo.Create(t.Context(), engagement, targets))

	for i := range 5 {
		at := baseTimestamp + domain.TimestampMs(i)*1_000
		require.NoError(t, events.Append(t.Context(),
			eventFixture(t, ids, engagement.ID, domain.EventNote, domain.LevelInfo, at)))
	}

	// When: only two are requested.
	list, err := events.ListByEngagement(t.Context(), engagement.ID, 2)
	require.NoError(t, err)

	// Then: the two newest come back.
	require.Len(t, list, 2)
	assert.Equal(t, baseTimestamp+4_000, list[0].CreatedAt)
	assert.Equal(t, baseTimestamp+3_000, list[1].CreatedAt)
}

func Test_EventListByEngagementAndLevel_filters_by_level(t *testing.T) {
	// Given: one warn event and two info events.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()
	engagement, targets := draftFixture(t, ids, "Event level filter", baseTimestamp)
	require.NoError(t, engagementRepo.Create(t.Context(), engagement, targets))

	require.NoError(t, events.Append(t.Context(),
		eventFixture(t, ids, engagement.ID, domain.EventNote, domain.LevelInfo, baseTimestamp)))
	require.NoError(t, events.Append(t.Context(),
		eventFixture(t, ids, engagement.ID, domain.EventError, domain.LevelWarn, baseTimestamp+1_000)))
	require.NoError(t, events.Append(t.Context(),
		eventFixture(t, ids, engagement.ID, domain.EventNote, domain.LevelInfo, baseTimestamp+2_000)))

	// When: warnings are requested.
	warnings, err := events.ListByEngagementAndLevel(t.Context(), engagement.ID, domain.LevelWarn, 10)
	require.NoError(t, err)

	// Then: only the warn row is returned.
	require.Len(t, warnings, 1)
	assert.Equal(t, domain.LevelWarn, warnings[0].Level)
	assert.Equal(t, domain.EventError, warnings[0].Kind)

	// Then: the unfiltered feed still holds all three.
	all, err := events.ListByEngagement(t.Context(), engagement.ID, 10)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func Test_EventListByEngagement_is_scoped_to_one_engagement(t *testing.T) {
	// Given: two engagements, each with its own event.
	db := openTestDB(t)
	engagementRepo := store.NewEngagementRepo(db)
	events := store.NewEventRepo(db)
	ids := domain.NewULIDGenerator()

	first, firstTargets := draftFixture(t, ids, "First engagement", baseTimestamp)
	second, secondTargets := draftFixture(t, ids, "Second engagement", baseTimestamp+1_000)
	require.NoError(t, engagementRepo.Create(t.Context(), first, firstTargets))
	require.NoError(t, engagementRepo.Create(t.Context(), second, secondTargets))

	require.NoError(t, events.Append(t.Context(),
		eventFixture(t, ids, first.ID, domain.EventNote, domain.LevelInfo, baseTimestamp)))
	require.NoError(t, events.Append(t.Context(),
		eventFixture(t, ids, second.ID, domain.EventNote, domain.LevelInfo, baseTimestamp+1_000)))

	// When: the first engagement's feed is read.
	list, err := events.ListByEngagement(t.Context(), first.ID, 10)
	require.NoError(t, err)

	// Then: only its own event appears.
	require.Len(t, list, 1)
	assert.Equal(t, first.ID.String(), list[0].EngagementID.String())
}
