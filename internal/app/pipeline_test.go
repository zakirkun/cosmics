package app_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/app"
	"github.com/cosmics/cosmics/internal/clock"
	"github.com/cosmics/cosmics/internal/store"
)

// openTestDB opens a migrated database inside the test's temporary directory.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "cosmics.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

func Test_BuildPipeline_assembles_every_analysis_stage_when_given_a_database(t *testing.T) {
	// Given
	db := openTestDB(t)
	fixed := clock.NewFixed(1_760_000_000_000)

	// When
	set, err := app.BuildPipeline(db, fixed)

	// Then
	require.NoError(t, err)
	require.NotNil(t, set.Runner)
	assert.Len(t, set.Stages, 3, "OSINT, Analysis and Mapper must all be registered")

	kinds := make([]string, 0, len(set.Stages))
	for _, stage := range set.Stages {
		kinds = append(kinds, string(stage.Kind()))
	}
	assert.Equal(t, []string{"osint", "analysis", "mapper"}, kinds,
		"stages must be registered in pipeline order")
}

func Test_BuildPipeline_rejects_a_missing_database_when_given_nil(t *testing.T) {
	// Given
	fixed := clock.NewFixed(1_760_000_000_000)

	// When
	_, err := app.BuildPipeline(nil, fixed)

	// Then
	require.ErrorIs(t, err, app.ErrPipelineIncomplete)
	assert.Contains(t, err.Error(), "database")
}

func Test_BuildPipeline_rejects_a_missing_clock_when_given_nil(t *testing.T) {
	// Given
	db := openTestDB(t)

	// When
	_, err := app.BuildPipeline(db, nil)

	// Then
	require.ErrorIs(t, err, app.ErrPipelineIncomplete)
	assert.Contains(t, err.Error(), "clock")
}

func Test_BuildPipeline_excludes_reporting_from_the_runner_when_built(t *testing.T) {
	// Given: reporting renders finished work rather than producing it, so it must
	// not be a stage that could mark an engagement complete by writing about it.
	db := openTestDB(t)
	fixed := clock.NewFixed(1_760_000_000_000)

	// When
	set, err := app.BuildPipeline(db, fixed)
	require.NoError(t, err)

	// Then
	for _, stage := range set.Stages {
		assert.NotEqual(t, "reporting", string(stage.Kind()),
			"reporting is a command over the data, not a pipeline stage")
	}
}
