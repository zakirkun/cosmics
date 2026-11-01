package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_StageKind_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.StageKind
		want  bool
	}{
		{name: "recon", stage: domain.StageRecon, want: true},
		{name: "osint", stage: domain.StageOSINT, want: true},
		{name: "analysis", stage: domain.StageAnalysis, want: true},
		{name: "mapper", stage: domain.StageMapper, want: true},
		{name: "exploit", stage: domain.StageExploit, want: true},
		{name: "reporting", stage: domain.StageReporting, want: true},
		{name: "empty", stage: domain.StageKind(""), want: false},
		{name: "unknown", stage: domain.StageKind("bogus"), want: false},
		{name: "engage pointer", stage: domain.StageKind("none"), want: false},
		{name: "wrong case", stage: domain.StageKind("Recon"), want: false},
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

func Test_StageKind_Title_ReturnsHumanName_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.StageKind
		want  string
	}{
		{name: "recon", stage: domain.StageRecon, want: "Recon"},
		{name: "osint", stage: domain.StageOSINT, want: "OSINT"},
		{name: "analysis", stage: domain.StageAnalysis, want: "Analysis"},
		{name: "mapper", stage: domain.StageMapper, want: "Mapper"},
		{name: "exploit", stage: domain.StageExploit, want: "Exploit"},
		{name: "reporting", stage: domain.StageReporting, want: "Reporting"},
		{name: "unknown echoes input", stage: domain.StageKind("bogus"), want: "bogus"},
		{name: "empty echoes input", stage: domain.StageKind(""), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			stage := tt.stage

			// When
			got := stage.Title()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_StageKind_Next_ReturnsFollowingStage_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name   string
		stage  domain.StageKind
		want   domain.StageKind
		wantOK bool
	}{
		{name: "recon", stage: domain.StageRecon, want: domain.StageOSINT, wantOK: true},
		{name: "osint", stage: domain.StageOSINT, want: domain.StageAnalysis, wantOK: true},
		{name: "analysis", stage: domain.StageAnalysis, want: domain.StageMapper, wantOK: true},
		{name: "mapper", stage: domain.StageMapper, want: domain.StageExploit, wantOK: true},
		{name: "exploit", stage: domain.StageExploit, want: domain.StageReporting, wantOK: true},
		{name: "reporting is last", stage: domain.StageReporting, want: "", wantOK: false},
		{name: "unknown", stage: domain.StageKind("bogus"), want: "", wantOK: false},
		{name: "empty", stage: domain.StageKind(""), want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			stage := tt.stage

			// When
			got, ok := stage.Next()

			// Then
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func Test_StageKind_Next_ReachesEveryLaterStage_when_WalkedFromTheFirst(t *testing.T) {
	// Given
	stage := domain.AllStages[0]

	// When & Then
	visited := []domain.StageKind{stage}
	for range domain.AllStages {
		next, ok := stage.Next()
		if !ok {
			break
		}
		visited = append(visited, next)
		stage = next
	}

	require.Equal(t, domain.AllStages, visited)
}

func Test_StageKind_Position_ReturnsOneBasedIndex_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		stage domain.StageKind
		want  int
	}{
		{name: "recon", stage: domain.StageRecon, want: 1},
		{name: "osint", stage: domain.StageOSINT, want: 2},
		{name: "analysis", stage: domain.StageAnalysis, want: 3},
		{name: "mapper", stage: domain.StageMapper, want: 4},
		{name: "exploit", stage: domain.StageExploit, want: 5},
		{name: "reporting", stage: domain.StageReporting, want: 6},
		{name: "unknown", stage: domain.StageKind("bogus"), want: 0},
		{name: "empty", stage: domain.StageKind(""), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			stage := tt.stage

			// When
			got := stage.Position()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_StageKind_AllStages_HoldValidUniqueValues_when_Enumerated(t *testing.T) {
	// Given
	stages := domain.AllStages

	// When & Then
	require.Len(t, stages, 6)

	seen := make(map[domain.StageKind]struct{}, len(stages))
	for i, stage := range stages {
		require.True(t, stage.Valid(), "stage %q at index %d must be declared", stage, i)
		require.Equal(t, i+1, stage.Position())

		_, duplicate := seen[stage]
		require.False(t, duplicate, "stage %q must appear once", stage)
		seen[stage] = struct{}{}
	}
}
