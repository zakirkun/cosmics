package report_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/report"
)

func Test_ParseVector_accepts_a_canonical_v31_vector_when_well_formed(t *testing.T) {
	// Given
	raw := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"

	// When
	vector, err := report.ParseVector(raw)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "N", vector.AttackVector)
	assert.Equal(t, "L", vector.AttackComplexity)
	assert.Equal(t, "N", vector.PrivilegesRequired)
	assert.Equal(t, "N", vector.UserInteraction)
	assert.Equal(t, "U", vector.Scope)
	assert.Equal(t, "H", vector.Confidentiality)
	assert.Equal(t, "H", vector.Integrity)
	assert.Equal(t, "H", vector.Availability)
}

func Test_ParseVector_rejects_a_missing_prefix_when_malformed(t *testing.T) {
	// Given
	raw := "AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"

	// When
	_, err := report.ParseVector(raw)

	// Then
	require.ErrorIs(t, err, report.ErrInvalidVector)
}

func Test_ParseVector_rejects_a_v4_vector_when_given_one(t *testing.T) {
	// Given
	raw := "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"

	// When
	_, err := report.ParseVector(raw)

	// Then
	require.ErrorIs(t, err, report.ErrUnsupportedVersion)
	assert.Contains(t, err.Error(), "v4.0")
}

func Test_ParseVector_rejects_an_out_of_range_metric_when_given_one(t *testing.T) {
	// Given
	raw := "CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"

	// When
	_, err := report.ParseVector(raw)

	// Then
	require.ErrorIs(t, err, report.ErrInvalidVector)
	assert.Contains(t, err.Error(), "AV")
}

func Test_ParseVector_rejects_a_missing_metric_when_incomplete(t *testing.T) {
	// Given
	raw := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H"

	// When
	_, err := report.ParseVector(raw)

	// Then
	require.ErrorIs(t, err, report.ErrInvalidVector)
	assert.Contains(t, err.Error(), "A")
}

func Test_ParseVector_rejects_a_duplicate_metric_when_repeated(t *testing.T) {
	// Given
	raw := "CVSS:3.1/AV:N/AV:L/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"

	// When
	_, err := report.ParseVector(raw)

	// Then
	require.ErrorIs(t, err, report.ErrInvalidVector)
	assert.Contains(t, err.Error(), "repeats")
}

func Test_BaseScore_matches_the_specification_for_known_vectors(t *testing.T) {
	// Given: base vectors with the scores published in the CVSS v3.1
	// specification and its examples. A wrong formula would put a wrong severity
	// on every finding, so these are the authority for this file.
	tests := []struct {
		name   string
		vector string
		want   float64
	}{
		{
			name:   "the worked example from the specification",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
			want:   9.8,
		},
		{
			name:   "scope changed raises the impact",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H",
			want:   10.0,
		},
		{
			name:   "local physical access with high complexity",
			vector: "CVSS:3.1/AV:P/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N",
			want:   1.6,
		},
		{
			name:   "adjacent network with required interaction",
			vector: "CVSS:3.1/AV:A/AC:L/PR:N/UI:R/S:U/C:H/I:N/A:N",
			want:   5.7,
		},
		{
			name:   "changed scope with low privileges",
			vector: "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:C/C:L/I:L/A:N",
			want:   6.4,
		},
		{
			name:   "no impact at all scores zero",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N",
			want:   0.0,
		},
		{
			name:   "local access high privileges low confidentiality",
			vector: "CVSS:3.1/AV:L/AC:L/PR:H/UI:N/S:U/C:L/I:N/A:N",
			want:   2.3,
		},
		{
			name:   "network high complexity no privileges",
			vector: "CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:N/A:N",
			want:   5.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			vector, err := report.ParseVector(tt.vector)
			require.NoError(t, err)

			// When
			score, err := vector.BaseScore()

			// Then
			require.NoError(t, err)
			assert.InDelta(t, tt.want, score, 0.0001,
				"vector %s must score %.1f, got %.1f", tt.vector, tt.want, score)
		})
	}
}

func Test_SeverityFromScore_returns_the_documented_band_when_given_boundary_scores(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.0, "info"},
		{0.1, "low"},
		{3.9, "low"},
		{4.0, "medium"},
		{6.9, "medium"},
		{7.0, "high"},
		{8.9, "high"},
		{9.0, "critical"},
		{10.0, "critical"},
	}

	for _, tt := range tests {
		t.Run(report.FormatScore(tt.score), func(t *testing.T) {
			// When
			got := report.SeverityFromScore(tt.score)

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_FormatScore_renders_one_decimal_when_given_a_whole_number(t *testing.T) {
	// When
	got := report.FormatScore(9)

	// Then
	assert.Equal(t, "9.0", got)
}

func Test_BaseScore_is_deterministic_when_computed_twice(t *testing.T) {
	// Given
	vector, err := report.ParseVector("CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H")
	require.NoError(t, err)

	// When
	first, err := vector.BaseScore()
	require.NoError(t, err)
	second, err := vector.BaseScore()
	require.NoError(t, err)

	// Then
	assert.InDelta(t, first, second, 0)
}
