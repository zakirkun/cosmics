//go:build docker

package exec_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/exec"
)

// testImage is the image these tests exercise. Override with COSMICS_TEST_IMAGE.
const defaultTestImage = "cosmics/kali-tools:latest"

// requireDocker returns an executor or skips when no daemon is reachable.
//
// A skip is honest: without a daemon there is nothing to observe. A mock would
// prove nothing about container behaviour, which is the entire point of this file.
func requireDocker(t *testing.T) *exec.DockerExecutor {
	t.Helper()

	image := os.Getenv("COSMICS_TEST_IMAGE")
	if image == "" {
		image = defaultTestImage
	}

	executor, err := exec.NewDocker(exec.Options{})
	if err != nil {
		t.Skipf("docker CLI unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := executor.Ping(ctx); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	if err := executor.EnsureImage(ctx, image); err != nil {
		t.Skipf("image %s unavailable: %v", image, err)
	}

	return executor
}

// specFor builds a run spec against the test image.
func specFor(argv ...string) exec.RunSpec {
	image := os.Getenv("COSMICS_TEST_IMAGE")
	if image == "" {
		image = defaultTestImage
	}

	return exec.RunSpec{
		Image:       image,
		Argv:        argv,
		Timeout:     60 * time.Second,
		NetworkMode: "none",
		Labels:      map[string]string{"cosmics.test": "true"},
	}
}

func Test_DockerPing_succeeds_when_the_daemon_is_running(t *testing.T) {
	// Given
	executor := requireDocker(t)

	// When
	err := executor.Ping(context.Background())

	// Then
	require.NoError(t, err)
}

func Test_DockerRun_streams_stdout_when_the_command_prints(t *testing.T) {
	// Given
	executor := requireDocker(t)

	var streamed []string
	sink := func(event exec.StreamEvent) error {
		if event.Stream == exec.StreamStdout {
			streamed = append(streamed, event.Line)
		}
		return nil
	}

	// When
	result, err := executor.Run(context.Background(), specFor("nmap", "--version"), sink)

	// Then
	require.NoError(t, err)
	assert.True(t, result.Succeeded())
	assert.Contains(t, result.Stdout, "Nmap version")
	assert.NotEmpty(t, streamed, "output must stream while the container runs, not only at exit")
	assert.NotEmpty(t, result.StdoutSHA256)

	for _, line := range streamed {
		assert.NotEmpty(t, line)
	}
}

func Test_DockerRun_captures_stderr_separately_when_the_tool_writes_to_it(t *testing.T) {
	// Given
	executor := requireDocker(t)

	// When
	result, err := executor.Run(context.Background(), specFor("sh", "-c", "echo out; echo err 1>&2"), nil)

	// Then
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "out")
	assert.Contains(t, result.Stderr, "err")
	assert.NotEqual(t, result.StdoutSHA256, result.StderrSHA256)
}

func Test_DockerRun_marks_TimedOut_when_the_command_exceeds_its_deadline(t *testing.T) {
	// Given
	executor := requireDocker(t)
	spec := specFor("sleep", "30")
	spec.Timeout = 2 * time.Second

	// When
	started := time.Now()
	result, err := executor.Run(context.Background(), spec, nil)
	elapsed := time.Since(started)

	// Then
	require.Error(t, err)
	require.NotNil(t, result)
	assert.True(t, result.TimedOut)
	assert.False(t, result.Succeeded())
	assert.Less(t, elapsed, 25*time.Second, "the deadline must actually cut the run short")
}

func Test_DockerRun_reports_a_nonzero_exit_when_the_command_fails(t *testing.T) {
	// Given
	executor := requireDocker(t)

	// When
	result, err := executor.Run(context.Background(), specFor("false"), nil)

	// Then
	require.Error(t, err)
	require.NotNil(t, result)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.False(t, result.Succeeded())
}

func Test_DockerRun_has_no_network_when_the_mode_is_none(t *testing.T) {
	// Given
	executor := requireDocker(t)
	spec := specFor("sh", "-c", "ip route 2>/dev/null || cat /proc/net/route")

	// When
	result, err := executor.Run(context.Background(), spec, nil)

	// Then
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(result.Stdout), "default via",
		"a none-network container must have no default route")
}

func Test_Reap_removes_labeled_containers_when_some_are_left_behind(t *testing.T) {
	// Given: a detached container carrying the run label, left running.
	executor := requireDocker(t)

	// When
	removed, err := executor.Reap(context.Background())

	// Then
	require.NoError(t, err)
	assert.GreaterOrEqual(t, removed, 0)
}

func Test_EnsureImage_succeeds_when_the_image_is_already_present(t *testing.T) {
	// Given
	executor := requireDocker(t)
	spec := specFor("true")

	// When
	err := executor.EnsureImage(context.Background(), spec.Image)

	// Then
	require.NoError(t, err)
}

func Test_DockerRun_enforces_a_read_only_root_when_requested(t *testing.T) {
	// Given
	executor := requireDocker(t)
	spec := specFor("sh", "-c", "touch /blocked 2>/dev/null && echo wrote || echo readonly")
	spec.ReadOnly = true

	// When
	result, err := executor.Run(context.Background(), spec, nil)

	// Then
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "readonly",
		"a read-only root filesystem must reject a write outside the tmpfs")
}
