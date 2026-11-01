package domain_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Mode_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name string
		mode domain.Mode
		want bool
	}{
		{name: "recon", mode: domain.ModeRecon, want: true},
		{name: "assess", mode: domain.ModeAssess, want: true},
		{name: "exploit", mode: domain.ModeExploit, want: true},
		{name: "full", mode: domain.ModeFull, want: true},
		{name: "empty", mode: domain.Mode(""), want: false},
		{name: "unknown", mode: domain.Mode("bogus"), want: false},
		{name: "wrong case", mode: domain.Mode("Recon"), want: false},
		{name: "padded", mode: domain.Mode(" recon "), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			mode := tt.mode

			// When
			got := mode.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Mode_AllowsExploitation_Reports_when_ModeIsDeclared(t *testing.T) {
	tests := []struct {
		name string
		mode domain.Mode
		want bool
	}{
		{name: "recon", mode: domain.ModeRecon, want: false},
		{name: "assess", mode: domain.ModeAssess, want: false},
		{name: "exploit", mode: domain.ModeExploit, want: true},
		{name: "full", mode: domain.ModeFull, want: true},
		{name: "empty", mode: domain.Mode(""), want: false},
		{name: "unknown", mode: domain.Mode("bogus"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			mode := tt.mode

			// When
			got := mode.AllowsExploitation()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Mode_AllowsPostExploitation_Reports_when_ModeIsDeclared(t *testing.T) {
	tests := []struct {
		name string
		mode domain.Mode
		want bool
	}{
		{name: "recon", mode: domain.ModeRecon, want: false},
		{name: "assess", mode: domain.ModeAssess, want: false},
		{name: "exploit", mode: domain.ModeExploit, want: false},
		{name: "full", mode: domain.ModeFull, want: true},
		{name: "empty", mode: domain.Mode(""), want: false},
		{name: "unknown", mode: domain.Mode("bogus"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			mode := tt.mode

			// When
			got := mode.AllowsPostExploitation()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Mode_Stages_ReturnsExactPipeline_when_ModeIsDeclared(t *testing.T) {
	reconPipeline := []domain.StageKind{domain.StageRecon, domain.StageOSINT, domain.StageReporting}
	assessPipeline := []domain.StageKind{
		domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
		domain.StageMapper, domain.StageReporting,
	}
	fullPipeline := []domain.StageKind{
		domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
		domain.StageMapper, domain.StageExploit, domain.StageReporting,
	}

	tests := []struct {
		name string
		mode domain.Mode
		want []domain.StageKind
	}{
		{name: "recon", mode: domain.ModeRecon, want: reconPipeline},
		{name: "assess", mode: domain.ModeAssess, want: assessPipeline},
		{name: "exploit", mode: domain.ModeExploit, want: fullPipeline},
		{name: "full", mode: domain.ModeFull, want: fullPipeline},
		{name: "empty", mode: domain.Mode(""), want: nil},
		{name: "unknown", mode: domain.Mode("bogus"), want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			mode := tt.mode

			// When
			got := mode.Stages()

			// Then
			assert.Equal(t, tt.want, got)
			if !assert.Len(t, got, len(tt.want)) {
				return
			}
			for i, wantStage := range tt.want {
				assert.Equal(t, wantStage, got[i], "stage at index %d", i)
			}
			for _, stage := range got {
				assert.True(t, stage.Valid(), "returned stage %q must be declared", stage)
			}
			if tt.want == nil {
				assert.Nil(t, got)
			}
		})
	}
}

func Test_Mode_Stages_KeepsCanonicalOrder_when_ModeIsDeclared(t *testing.T) {
	// Given
	modes := []domain.Mode{domain.ModeRecon, domain.ModeAssess, domain.ModeExploit, domain.ModeFull}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			// When
			stages := mode.Stages()

			// Then
			require.NotEmpty(t, stages)

			lastPos := 0
			for _, stage := range stages {
				pos := stage.Position()
				require.Positive(t, pos, "stage %q must have a canonical position", stage)
				require.Greater(t, pos, lastPos, "stages must follow canonical order")
				lastPos = pos
			}
		})
	}
}

func Test_Mode_IncludesStage_Reports_when_StageIsDeclared(t *testing.T) {
	tests := []struct {
		name    string
		mode    domain.Mode
		allowed []domain.StageKind
	}{
		{
			name:    "recon",
			mode:    domain.ModeRecon,
			allowed: []domain.StageKind{domain.StageRecon, domain.StageOSINT, domain.StageReporting},
		},
		{
			name: "assess",
			mode: domain.ModeAssess,
			allowed: []domain.StageKind{
				domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
				domain.StageMapper, domain.StageReporting,
			},
		},
		{
			name: "exploit",
			mode: domain.ModeExploit,
			allowed: []domain.StageKind{
				domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
				domain.StageMapper, domain.StageExploit, domain.StageReporting,
			},
		},
		{
			name: "full",
			mode: domain.ModeFull,
			allowed: []domain.StageKind{
				domain.StageRecon, domain.StageOSINT, domain.StageAnalysis,
				domain.StageMapper, domain.StageExploit, domain.StageReporting,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, stage := range domain.AllStages {
				// Given
				want := slices.Contains(tt.allowed, stage)

				// When
				got := tt.mode.IncludesStage(stage)

				// Then
				assert.Equal(t, want, got, "mode %s stage %s", tt.mode, stage)
			}

			// Unknown stages are never included.
			assert.False(t, tt.mode.IncludesStage(domain.StageKind("bogus")))
			assert.False(t, tt.mode.IncludesStage(domain.StageKind("")))
		})
	}
}
