package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// findingFixture builds a valid finding at one severity.
func findingFixture(engagementID domain.ID, title, severity string) store.Finding {
	return store.Finding{
		EngagementID: engagementID,
		Title:        title,
		Category:     "web",
		Severity:     severity,
		Confidence:   "possible",
		Description:  "Deterministic hypothesis recorded by the analysis pass.",
		Status:       "open",
		CreatedAt:    baseTimestamp,
		UpdatedAt:    baseTimestamp,
	}
}

func Test_FindingRepo_List_orders_by_severity_rank_when_severities_differ(t *testing.T) {
	// Given findings inserted with medium first, then high, then critical
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "finding-rank")
	repo := store.NewFindingRepo(db)

	for _, fixture := range []struct{ title, severity string }{
		{"Medium issue", "medium"},
		{"Critical issue", "critical"},
		{"High issue", "high"},
		{"Low issue", "low"},
		{"Info note", "info"},
	} {
		_, err := repo.Create(ctx, findingFixture(engagement.ID, fixture.title, fixture.severity))
		require.NoError(t, err)
	}

	// When
	findings, err := repo.List(ctx, engagement.ID)

	// Then the order is by rank, not by spelling
	require.NoError(t, err)
	require.Len(t, findings, 5)
	got := make([]string, 0, len(findings))
	for _, finding := range findings {
		got = append(got, finding.Severity)
	}
	assert.Equal(t, []string{"critical", "high", "medium", "low", "info"}, got,
		"a lexical sort would put high before medium")
}

func Test_FindingRepo_Create_rejects_an_unknown_category_when_given_one(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "finding-category")
	repo := store.NewFindingRepo(db)

	finding := findingFixture(engagement.ID, "Bogus", "high")
	finding.Category = "not_a_category"

	// When
	_, err := repo.Create(ctx, finding)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrInvalidFinding)
	assert.Contains(t, err.Error(), "category")
}

func Test_FindingRepo_CountBySeverity_counts_each_level_when_several_exist(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "finding-counts")
	repo := store.NewFindingRepo(db)

	for _, fixture := range []struct{ title, severity string }{
		{"One critical", "critical"},
		{"Two high", "high"},
		{"Three high", "high"},
		{"Four medium", "medium"},
	} {
		_, err := repo.Create(ctx, findingFixture(engagement.ID, fixture.title, fixture.severity))
		require.NoError(t, err)
	}

	// When
	counts, err := repo.CountBySeverity(ctx, engagement.ID)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, counts["critical"])
	assert.Equal(t, 2, counts["high"])
	assert.Equal(t, 1, counts["medium"])
	assert.Equal(t, 0, counts["low"], "an absent level must still be present as zero")
	assert.Equal(t, 0, counts["info"])
}

func Test_FindingRepo_SetStatus_rejects_an_unknown_status(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "finding-status")
	repo := store.NewFindingRepo(db)
	created, err := repo.Create(ctx, findingFixture(engagement.ID, "Status subject", "low"))
	require.NoError(t, err)

	// When
	statusErr := repo.SetStatus(ctx, created.ID, "wontfix", baseTimestamp+1000)

	// Then
	require.Error(t, statusErr)
	require.ErrorIs(t, statusErr, store.ErrInvalidFinding)
}

func Test_FindingRepo_Count_returns_zero_when_the_engagement_has_none(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "finding-empty")
	repo := store.NewFindingRepo(db)

	// When
	count, err := repo.Count(ctx, engagement.ID)

	// Then
	require.NoError(t, err)
	assert.Zero(t, count)
}
