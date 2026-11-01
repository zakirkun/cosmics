package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

func Test_Budget_Exhausted_is_true_when_iterations_reach_the_cap(t *testing.T) {
	// Given
	budget := pipeline.Budget{IterationsUsed: 40, IterationsMax: 40}

	// When
	got := budget.Exhausted()

	// Then
	assert.True(t, got)
}

func Test_Budget_Exhausted_is_false_when_every_dimension_has_room(t *testing.T) {
	// Given
	budget := pipeline.Budget{IterationsUsed: 3, IterationsMax: 40, TokensUsed: 100, TokensMax: 10000}

	// When
	got := budget.Exhausted()

	// Then
	assert.False(t, got)
}

func Test_Budget_Warn_is_true_when_usage_crosses_eighty_percent(t *testing.T) {
	// Given
	budget := pipeline.Budget{TokensUsed: 8, TokensMax: 10}

	// When
	got := budget.Warn()

	// Then
	assert.True(t, got)
}

func Test_Budget_Warn_is_false_when_usage_is_low(t *testing.T) {
	// Given
	budget := pipeline.Budget{TokensUsed: 1, TokensMax: 10}

	// When
	got := budget.Warn()

	// Then
	assert.False(t, got)
}

func Test_Allowed_reports_the_mode_decision_when_checked(t *testing.T) {
	tests := []struct {
		mode  domain.Mode
		stage domain.StageKind
		want  bool
	}{
		{domain.ModeRecon, domain.StageRecon, true},
		{domain.ModeRecon, domain.StageExploit, false},
		{domain.ModeAssess, domain.StageMapper, true},
		{domain.ModeAssess, domain.StageExploit, false},
		{domain.ModeExploit, domain.StageExploit, true},
		{domain.ModeFull, domain.StageExploit, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.mode, tt.stage), func(t *testing.T) {
			// When
			got := pipeline.Allowed(tt.mode, tt.stage)

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_ExitResult_reason_survives_when_unmet(t *testing.T) {
	// Given
	result := pipeline.Unmet("missing %s", "asset inventory")

	// When
	text := result.Reason

	// Then
	assert.False(t, result.Met)
	assert.True(t, strings.Contains(text, "asset inventory"))
}

func Test_Run_finishes_within_a_wall_clock_budget_when_stages_are_trivial(t *testing.T) {
	// Given
	store := newFakeStore()
	stages := make([]pipeline.Stage, 0, len(domain.AllStages))
	for _, kind := range domain.AllStages {
		stages = append(stages, passingStage(kind))
	}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: stages,
		Store:  store,
		Clock:  fixedClock{at: refNow},
	})
	require.NoError(t, err)

	// When
	started := time.Now()
	_, err = runner.Run(context.Background(), engagementFixture(t, domain.ModeFull))
	elapsed := time.Since(started)

	// Then
	require.NoError(t, err)
	assert.Less(t, elapsed, 2*time.Second, "stages must not sleep or block on I/O")
}
