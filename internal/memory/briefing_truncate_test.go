package memory_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/memory"
)

func Test_BuildBriefing_truncates_with_a_marker_when_output_exceeds_the_cap(t *testing.T) {
	// Given: an engagement with enough events and findings to exceed the cap.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)

	longTitle := strings.Repeat("exhaustive finding description ", 40)
	for i := range 40 {
		_, err := db.Writer().ExecContext(t.Context(),
			`INSERT INTO findings
			 (id, engagement_id, title, category, severity, confidence, description, status,
			  created_at, updated_at)
			 VALUES (?, ?, ?, 'web', 'high', 'likely', 'x', 'open', ?, ?)`,
			domain.NewULIDGenerator().NewAt(baseTimestamp+domain.TimestampMs(i)).String(),
			engagementID.String(), longTitle, int64(baseTimestamp), int64(baseTimestamp))
		require.NoError(t, err)
	}

	// When: the briefing is built.
	out, err := memory.BuildBriefing(t.Context(), memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp,
	})

	// Then: the output is capped and says so explicitly.
	require.NoError(t, err)
	assert.LessOrEqual(t, len(out), memory.BriefingMaxChars,
		"the briefing must never exceed its cap")
	assert.Contains(t, out, "… (truncated)",
		"truncation must be marked, never silent")
}

func Test_BuildBriefing_truncation_marker_is_stable_when_rebuilt(t *testing.T) {
	// Given: an over-cap database.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)

	longTitle := strings.Repeat("padding ", 500)
	for i := range 30 {
		_, err := db.Writer().ExecContext(t.Context(),
			`INSERT INTO findings
			 (id, engagement_id, title, category, severity, confidence, description, status,
			  created_at, updated_at)
			 VALUES (?, ?, ?, 'web', 'low', 'possible', 'x', 'open', ?, ?)`,
			domain.NewULIDGenerator().NewAt(baseTimestamp+domain.TimestampMs(i)).String(),
			engagementID.String(), longTitle, int64(baseTimestamp), int64(baseTimestamp))
		require.NoError(t, err)
	}

	deps := memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp,
	}

	// When: the truncated briefing is built twice.
	first, err := memory.BuildBriefing(t.Context(), deps)
	require.NoError(t, err)
	second, err := memory.BuildBriefing(t.Context(), deps)
	require.NoError(t, err)

	// Then: truncation is deterministic too.
	assert.Equal(t, first, second)
}

func Test_BuildBriefing_rejects_a_nil_database_when_called(t *testing.T) {
	// Given: no database.
	// When: a briefing is requested.
	_, err := memory.BuildBriefing(t.Context(), memory.BriefingDeps{
		Engagement: briefingEngagement(newEngagementID()),
		Now:        baseTimestamp,
	})

	// Then: it fails rather than panicking.
	require.Error(t, err)
}
