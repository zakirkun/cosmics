package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

func Test_NewRunner_rejects_a_missing_store_when_built(t *testing.T) {
	// Given
	opts := pipeline.RunnerOptions{Clock: fixedClock{at: refNow}}

	// When
	_, err := pipeline.NewRunner(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrStageMissing)
}

func Test_NewRunner_rejects_a_missing_clock_when_built(t *testing.T) {
	// Given
	opts := pipeline.RunnerOptions{Store: newFakeStore()}

	// When
	_, err := pipeline.NewRunner(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrStageMissing)
}

func Test_NewRunner_rejects_a_duplicate_stage_when_built(t *testing.T) {
	// Given
	opts := pipeline.RunnerOptions{
		Store:  newFakeStore(),
		Clock:  fixedClock{at: refNow},
		Stages: []pipeline.Stage{passingStage(domain.StageRecon), passingStage(domain.StageRecon)},
	}

	// When
	_, err := pipeline.NewRunner(opts)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, pipeline.ErrStageMissing)
}
