package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_StageRunStatus_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name   string
		status domain.StageRunStatus
		want   bool
	}{
		{name: "pending", status: domain.StagePending, want: true},
		{name: "running", status: domain.StageRunning, want: true},
		{name: "succeeded", status: domain.StageSucceeded, want: true},
		{name: "failed", status: domain.StageFailed, want: true},
		{name: "skipped", status: domain.StageSkipped, want: true},
		{name: "aborted", status: domain.StageAborted, want: true},
		{name: "empty", status: domain.StageRunStatus(""), want: false},
		{name: "unknown", status: domain.StageRunStatus("bogus"), want: false},
		{name: "engagement status", status: domain.StageRunStatus("draft"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			status := tt.status

			// When
			got := status.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_StageRunStatus_Terminal_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name   string
		status domain.StageRunStatus
		want   bool
	}{
		{name: "pending", status: domain.StagePending, want: false},
		{name: "running", status: domain.StageRunning, want: false},
		{name: "succeeded", status: domain.StageSucceeded, want: true},
		{name: "failed", status: domain.StageFailed, want: true},
		{name: "skipped", status: domain.StageSkipped, want: true},
		{name: "aborted", status: domain.StageAborted, want: true},
		{name: "empty", status: domain.StageRunStatus(""), want: false},
		{name: "unknown", status: domain.StageRunStatus("bogus"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			status := tt.status

			// When
			got := status.Terminal()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_AuditAction_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	declared := []domain.AuditAction{
		domain.AuditEngagementCreated,
		domain.AuditAuthorizationRecorded,
		domain.AuditTargetAdded,
		domain.AuditEngagementStatusChanged,
		domain.AuditToolExecuted,
		domain.AuditExploitLaunched,
		domain.AuditReportGenerated,
		domain.AuditConfigChanged,
	}

	tests := []struct {
		name   string
		action domain.AuditAction
		want   bool
	}{
		{name: "empty", action: domain.AuditAction(""), want: false},
		{name: "unknown", action: domain.AuditAction("bogus"), want: false},
	}

	for _, action := range declared {
		tests = append(tests, struct {
			name   string
			action domain.AuditAction
			want   bool
		}{name: string(action), action: action, want: true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			action := tt.action

			// When
			got := action.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}

	// Persisted values must be stable and unique.
	require.Len(t, declared, 8)
	seen := make(map[string]struct{}, len(declared))
	for _, action := range declared {
		_, duplicate := seen[string(action)]
		require.False(t, duplicate, "audit action %q must be unique", action)
		seen[string(action)] = struct{}{}
	}
}

func Test_Actor_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		actor domain.Actor
		want  bool
	}{
		{name: "operator", actor: domain.ActorOperator, want: true},
		{name: "agent", actor: domain.ActorAgent, want: true},
		{name: "system", actor: domain.ActorSystem, want: true},
		{name: "empty", actor: domain.Actor(""), want: false},
		{name: "unknown", actor: domain.Actor("bogus"), want: false},
		{name: "wrong case", actor: domain.Actor("Operator"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			actor := tt.actor

			// When
			got := actor.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_AuditOutcome_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name    string
		outcome domain.AuditOutcome
		want    bool
	}{
		{name: "allowed", outcome: domain.OutcomeAllowed, want: true},
		{name: "denied", outcome: domain.OutcomeDenied, want: true},
		{name: "error", outcome: domain.OutcomeError, want: true},
		{name: "empty", outcome: domain.AuditOutcome(""), want: false},
		{name: "unknown", outcome: domain.AuditOutcome("bogus"), want: false},
		{name: "wrong case", outcome: domain.AuditOutcome("Allowed"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			outcome := tt.outcome

			// When
			got := outcome.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_EngageStage_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.EngageStage
		want  bool
	}{
		{name: "none", stage: domain.EngageStageNone, want: true},
		{name: "recon", stage: domain.EngageStageRecon, want: true},
		{name: "osint", stage: domain.EngageStageOSINT, want: true},
		{name: "analysis", stage: domain.EngageStageAnalysis, want: true},
		{name: "mapper", stage: domain.EngageStageMapper, want: true},
		{name: "exploit", stage: domain.EngageStageExploit, want: true},
		{name: "reporting", stage: domain.EngageStageReporting, want: true},
		{name: "done", stage: domain.EngageStageDone, want: true},
		{name: "empty", stage: domain.EngageStage(""), want: false},
		{name: "unknown", stage: domain.EngageStage("bogus"), want: false},
		{name: "status value", stage: domain.EngageStage("draft"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			stage := tt.stage

			// When
			got := stage.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_EngageStage_StageKind_RoundTrips_when_PointerIsAStage(t *testing.T) {
	for _, stage := range domain.AllStages {
		t.Run(string(stage), func(t *testing.T) {
			// Given
			pointer := domain.EngageStageOf(stage)
			require.True(t, pointer.Valid())

			// When
			got, ok := pointer.StageKind()

			// Then
			assert.True(t, ok)
			assert.Equal(t, stage, got)

			// The persisted pointer string equals the stage string.
			assert.Equal(t, string(stage), string(pointer))
		})
	}
}

func Test_EngageStage_StageKind_ReturnsFalse_when_PointerIsNoneOrDone(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.EngageStage
	}{
		{name: "none", stage: domain.EngageStageNone},
		{name: "done", stage: domain.EngageStageDone},
		{name: "unknown", stage: domain.EngageStage("bogus")},
		{name: "empty", stage: domain.EngageStage("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			stage := tt.stage

			// When
			got, ok := stage.StageKind()

			// Then
			assert.False(t, ok)
			assert.Equal(t, domain.StageKind(""), got)
			assert.False(t, got.Valid())
		})
	}
}

func Test_EngageStageOf_ReturnsNone_when_StageIsUnknown(t *testing.T) {
	// Given
	unknown := []domain.StageKind{domain.StageKind("bogus"), domain.StageKind(""), domain.StageKind("done"), domain.StageKind("none")}

	for _, stage := range unknown {
		t.Run(string(stage), func(t *testing.T) {
			// When
			got := domain.EngageStageOf(stage)

			// Then
			assert.Equal(t, domain.EngageStageNone, got)
		})
	}
}

func Test_EngageStage_AllEngageStages_HoldValidUniqueValues_when_Enumerated(t *testing.T) {
	// Given
	stages := domain.AllEngageStages

	// When & Then
	require.Len(t, stages, 8)

	seen := make(map[domain.EngageStage]struct{}, len(stages))
	for _, stage := range stages {
		require.True(t, stage.Valid(), "engage stage %q must be declared", stage)

		_, duplicate := seen[stage]
		require.False(t, duplicate, "engage stage %q must appear once", stage)
		seen[stage] = struct{}{}
	}
}

// Exhaustiveness safety nets. These fail the moment a constant is added to an
// enumeration without teaching the corresponding Valid method about it.
func Test_AllModes_AreValid_when_Enumerated(t *testing.T) {
	// Given
	modes := domain.AllModes

	// When & Then
	require.Len(t, modes, 4)
	for _, mode := range modes {
		require.True(t, mode.Valid(), "mode %q must be valid", mode)
	}
}

func Test_AllStages_AreValid_when_Enumerated(t *testing.T) {
	// Given
	stages := domain.AllStages

	// When & Then
	require.Len(t, stages, 6)
	for _, stage := range stages {
		require.True(t, stage.Valid(), "stage %q must be valid", stage)
		require.NotEmpty(t, stage.Title())
	}
}

func Test_AllEngageStages_AreValid_when_Enumerated(t *testing.T) {
	// Given
	stages := domain.AllEngageStages

	// When & Then
	require.Len(t, stages, 8)
	for _, stage := range stages {
		require.True(t, stage.Valid(), "engage stage %q must be valid", stage)
	}
}
