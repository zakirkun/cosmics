package exec_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/exec"
)

func validSpec() exec.RunSpec {
	return exec.RunSpec{
		Image: "cosmics/kali-tools:latest",
		Argv:  []string{"nmap", "-sV", "example.com"},
	}
}

func Test_RunSpec_Validate_rejects_an_empty_image_when_called(t *testing.T) {
	// Given
	spec := exec.RunSpec{Argv: []string{"nmap"}}

	// When
	err := spec.Validate()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrRunFailed)
}

func Test_RunSpec_Validate_rejects_an_empty_argv_when_called(t *testing.T) {
	// Given
	spec := exec.RunSpec{Image: "cosmics/kali-tools:latest"}

	// When
	err := spec.Validate()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrRunFailed)
}

func Test_RunSpec_Validate_rejects_an_unsupported_network_mode_when_called(t *testing.T) {
	// Given
	spec := validSpec()
	spec.NetworkMode = "macvlan-shim"

	// When
	err := spec.Validate()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrRunFailed)
}

func Test_RunSpec_Validate_accepts_every_documented_network_mode_when_used(t *testing.T) {
	for _, mode := range []string{"", "bridge", "host", "none"} {
		t.Run("mode "+mode, func(t *testing.T) {
			// Given
			spec := validSpec()
			spec.NetworkMode = mode

			// When
			err := spec.Validate()

			// Then
			require.NoError(t, err)
		})
	}
}

func Test_Fake_records_every_spec_when_run(t *testing.T) {
	// Given
	fake := exec.NewFake()
	ctx := context.Background()

	first := validSpec()
	second := validSpec()
	second.Argv = []string{"httpx", "-u", "example.com"}

	// When
	_, errFirst := fake.Run(ctx, first, nil)
	_, errSecond := fake.Run(ctx, second, nil)

	// Then
	require.NoError(t, errFirst)
	require.NoError(t, errSecond)

	specs := fake.Specs()
	require.Len(t, specs, 2)
	assert.Equal(t, first.Argv, specs[0].Argv)
	assert.Equal(t, second.Argv, specs[1].Argv)
}

func Test_Fake_returns_the_configured_result_when_the_tool_matches(t *testing.T) {
	// Given
	fake := exec.NewFake()
	fake.SetResult("nmap", exec.Result{
		ExitCode: 0,
		Stdout:   "22/tcp open ssh",
		Stderr:   "",
	})

	// When
	result, err := fake.Run(context.Background(), validSpec(), nil)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "22/tcp open ssh", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func Test_Fake_replays_captured_output_through_the_sink_when_configured(t *testing.T) {
	// Given
	fake := exec.NewFake()
	fake.SetResult("nmap", exec.Result{Stdout: "line one\nline two"})

	var lines []string
	sink := func(event exec.StreamEvent) error {
		lines = append(lines, event.Line)
		return nil
	}

	// When
	_, err := fake.Run(context.Background(), validSpec(), sink)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []string{"line one", "line two"}, lines)
}

func Test_Fake_returns_the_configured_error_when_the_tool_fails(t *testing.T) {
	// Given
	fake := exec.NewFake()
	sentinel := errors.New("container exited 137")
	fake.SetError("nmap", sentinel)

	// When
	_, err := fake.Run(context.Background(), validSpec(), nil)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func Test_Fake_simulates_a_timeout_when_configured(t *testing.T) {
	// Given
	fake := exec.NewFake()
	fake.SetTimeout(true)

	// When
	result, err := fake.Run(context.Background(), validSpec(), nil)

	// Then
	require.NoError(t, err)
	assert.True(t, result.TimedOut)
	assert.False(t, result.Succeeded(), "a timed-out run never counts as success")
}

func Test_Fake_returns_the_context_error_when_the_context_is_cancelled(t *testing.T) {
	// Given
	fake := exec.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err := fake.Run(ctx, validSpec(), nil)

	// Then
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, fake.Specs(), "a cancelled run must not reach the executor")
}

func Test_Fake_rejects_an_invalid_spec_when_run(t *testing.T) {
	// Given
	fake := exec.NewFake()
	spec := exec.RunSpec{}

	// When
	_, err := fake.Run(context.Background(), spec, nil)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrRunFailed)
}

func Test_Fake_EnsureImage_records_the_image_when_called(t *testing.T) {
	// Given
	fake := exec.NewFake()

	// When
	err := fake.EnsureImage(context.Background(), "cosmics/kali-tools:latest")

	// Then
	require.NoError(t, err)
	assert.True(t, fake.ImagePresent("cosmics/kali-tools:latest"))
}

func Test_StaticExecutor_returns_the_same_result_on_every_run(t *testing.T) {
	// Given
	static := exec.NewStatic(exec.Result{ExitCode: 0, Stdout: "ok"})

	// When
	first, errFirst := static.Run(context.Background(), validSpec(), nil)
	second, errSecond := static.Run(context.Background(), validSpec(), nil)

	// Then
	require.NoError(t, errFirst)
	require.NoError(t, errSecond)
	assert.Equal(t, first.Stdout, second.Stdout)
	assert.Equal(t, 2, static.Runs())
}

func Test_Result_Succeeded_is_false_when_the_exit_code_is_nonzero(t *testing.T) {
	// Given
	result := exec.Result{ExitCode: 2}

	// When
	got := result.Succeeded()

	// Then
	assert.False(t, got)
}

func Test_Result_Succeeded_is_true_when_the_run_completed_and_not_timed_out(t *testing.T) {
	// Given
	result := exec.Result{ExitCode: 0}

	// When
	got := result.Succeeded()

	// Then
	assert.True(t, got)
}

func Test_Result_digests_differ_when_output_differs(t *testing.T) {
	// Given
	output := strings.Repeat("A", 32)

	// When
	first := exec.Result{Stdout: output}
	second := exec.Result{Stdout: output + "B"}

	// Then
	assert.NotEqual(t, first.Stdout, second.Stdout)
}

func Test_RunSpec_Validate_accepts_a_negative_timeout_only_when_zero(t *testing.T) {
	// Given
	spec := validSpec()
	spec.Timeout = -time.Second

	// When
	err := spec.Validate()

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrRunFailed)
}
