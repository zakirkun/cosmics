package doctor_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/doctor"
)

func Test_Report_Healthy_returns_true_when_no_check_failed(t *testing.T) {
	// Given
	report := doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.StatusOK},
		{Name: "b", Status: doctor.StatusWarn},
		{Name: "c", Status: doctor.StatusSkip},
	}}

	// When
	got := report.Healthy()

	// Then
	require.True(t, got)
}

func Test_Report_Healthy_returns_false_when_a_check_failed(t *testing.T) {
	// Given
	report := doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.StatusOK},
		{Name: "b", Status: doctor.StatusFail},
	}}

	// When
	got := report.Healthy()

	// Then
	require.False(t, got)
}

func Test_Report_Render_includes_hints_when_a_check_declares_them(t *testing.T) {
	// Given
	report := doctor.Report{Checks: []doctor.Check{{
		Name:   "docker",
		Status: doctor.StatusFail,
		Detail: "daemon not reachable",
		Hints:  []string{"start Docker Desktop"},
	}}}

	// When
	out := report.Render()

	// Then
	assert.Contains(t, out, "docker")
	assert.Contains(t, out, "daemon not reachable")
	assert.Contains(t, out, "start Docker Desktop")
	assert.Contains(t, out, "1 failure(s)")
}

func Test_Report_Render_reports_success_when_every_check_passed(t *testing.T) {
	// Given
	report := doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.StatusOK, Detail: "fine"},
	}}

	// When
	out := report.Render()

	// Then
	assert.Contains(t, out, "all checks passed")
}

func Test_Run_reports_a_missing_gateway_when_no_provider_is_configured(t *testing.T) {
	// Given
	cfg := config.Config{}

	// When
	report, err := doctor.Run(context.Background(), cfg)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, report.Checks)

	var gateway *doctor.Check
	for i := range report.Checks {
		if report.Checks[i].Name == "llm gateway" {
			gateway = &report.Checks[i]
			break
		}
	}
	require.NotNil(t, gateway, "doctor must always report on the llm gateway")
	require.Equal(t, doctor.StatusFail, gateway.Status)
	assert.Contains(t, gateway.Detail, "no provider configured")
}

func Test_Run_reports_a_config_failure_when_the_config_is_invalid(t *testing.T) {
	// Given
	cfg := config.Config{}

	// When
	report, err := doctor.Run(context.Background(), cfg)

	// Then
	require.NoError(t, err)

	var configuration *doctor.Check
	for i := range report.Checks {
		if report.Checks[i].Name == "configuration" {
			configuration = &report.Checks[i]
			break
		}
	}
	require.NotNil(t, configuration)
	require.Equal(t, doctor.StatusFail, configuration.Status)
	require.NotEmpty(t, configuration.Hints)
	assert.Contains(t, strings.Join(configuration.Hints, " "), "provider")
}

func Test_Run_reports_the_data_directory_as_available_when_it_can_be_created(t *testing.T) {
	// Given
	cfg := config.Config{}
	cfg.App.DataDir = t.TempDir()

	// When
	report, err := doctor.Run(context.Background(), cfg)

	// Then
	require.NoError(t, err)

	var dataDir *doctor.Check
	for i := range report.Checks {
		if report.Checks[i].Name == "data directory" {
			dataDir = &report.Checks[i]
			break
		}
	}
	require.NotNil(t, dataDir)
	require.Equal(t, doctor.StatusOK, dataDir.Status)
}

func Test_StorePath_joins_the_database_name_when_given_a_directory(t *testing.T) {
	// Given
	dir := t.TempDir()

	// When
	got := doctor.StorePath(dir)

	// Then
	assert.Contains(t, got, "cosmics.db")
	assert.True(t, strings.HasPrefix(got, dir))
}
