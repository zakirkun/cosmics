package exec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
)

// Defaults for container resource limits.
const (
	DefaultMemoryMB  = 1024
	DefaultCPUs      = 2.0
	DefaultPIDsLimit = 512
	DefaultTimeout   = 300 * time.Second

	// killGrace bounds how long docker kill may take before the run is abandoned.
	killGrace = 15 * time.Second
)

// RunLabel marks every container this executor creates, so orphans are findable
// even after the CLI that started them has died.
const RunLabel = "cosmics.run"

// Options configures a DockerExecutor.
type Options struct {
	// DockerPath is the docker executable. Empty means "docker" from PATH.
	DockerPath string

	// Clock supplies timestamps. Nil uses the system clock.
	Clock domain.Clock

	// WorkspaceHost is the host directory bound into the container at /work.
	WorkspaceHost string
}

// DockerExecutor runs tools through the docker CLI.
type DockerExecutor struct {
	docker    string
	clock     domain.Clock
	workspace string
}

// systemClock reads the host time source.
type systemClock struct{}

// Now returns the current instant in milliseconds.
func (systemClock) Now() domain.TimestampMs {
	return domain.TimestampMs(time.Now().UTC().UnixMilli())
}

// NewDocker returns an executor that shells out to the docker CLI.
func NewDocker(opts Options) (*DockerExecutor, error) {
	dockerPath := opts.DockerPath
	if dockerPath == "" {
		resolved, err := exec.LookPath("docker")
		if err != nil {
			return nil, fmt.Errorf("locate docker: %w: %w", ErrDockerUnavailable, err)
		}
		dockerPath = resolved
	}

	clock := opts.Clock
	if clock == nil {
		clock = systemClock{}
	}

	return &DockerExecutor{
		docker:    dockerPath,
		clock:     clock,
		workspace: opts.WorkspaceHost,
	}, nil
}

// Ping verifies that the daemon answers.
func (d *DockerExecutor) Ping(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, d.docker, "version", "--format", "{{.Server.Version}}") // #nosec G204 -- docker path is operator-configured and args are constants
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker daemon unreachable: %w: %w", ErrDockerUnavailable, err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return fmt.Errorf("docker reported an empty server version: %w", ErrDockerUnavailable)
	}
	return nil
}

// EnsureImage checks for the image and pulls it when absent.
func (d *DockerExecutor) EnsureImage(ctx context.Context, image string) error {
	if image == "" {
		return fmt.Errorf("ensure image: image is empty: %w", ErrImageMissing)
	}

	inspect := exec.CommandContext(ctx, d.docker, "image", "inspect", image) // #nosec G204 -- image is validated by RunSpec.Validate to reject leading "-"
	if err := inspect.Run(); err == nil {
		return nil
	}

	pull := exec.CommandContext(ctx, d.docker, "pull", image) // #nosec G204 -- image is validated by RunSpec.Validate to reject leading "-"
	if output, err := pull.CombinedOutput(); err != nil {
		return fmt.Errorf("pull %s: %w: %s", image, ErrImageMissing, strings.TrimSpace(string(output)))
	}
	return nil
}

// Run starts a container, streams its output, and enforces the deadline.
//
// A timeout is enforced by killing the container by its run label, not by killing
// the CLI: the CLI exiting does not stop the container, so relying on it would
// leave a scan running after the caller has moved on.
func (d *DockerExecutor) Run(ctx context.Context, spec RunSpec, sink StreamSink) (*Result, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	runID := fmt.Sprintf("cosmics-%d", d.clock.Now())
	args := d.buildArgs(spec, runID)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	started := d.clock.Now()
	startedWall := time.Now()

	cmd := exec.CommandContext(runCtx, d.docker, args...) // #nosec G204 -- argv comes from validated RunSpec fields; no shell

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("attach stdout: %w: %w", ErrRunFailed, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("attach stderr: %w: %w", ErrRunFailed, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start container: %w: %w", ErrDockerUnavailable, err)
	}

	collector := newOutputCollector(spec.MaxOutput())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		collector.consume(stdoutPipe, StreamStdout, sink, d.clock)
	}()
	go func() {
		defer wg.Done()
		collector.consume(stderrPipe, StreamStderr, sink, d.clock)
	}()

	timedOut := false
	deadline := time.AfterFunc(timeout, func() {
		timedOut = true
		cancel()
		d.killByLabel(ctx, runID)
	})

	waitErr := cmd.Wait()
	deadline.Stop()

	wg.Wait()

	finished := d.clock.Now()

	result := collector.result()
	result.StartedAt = started
	result.FinishedAt = finished
	result.Duration = time.Since(startedWall)
	result.TimedOut = timedOut

	var exitErr *exec.ExitError
	switch {
	case timedOut:
		return &result, fmt.Errorf("container exceeded %s: %w", timeout, ErrRunFailed)
	case errors.As(waitErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()
		return &result, fmt.Errorf("container exited %d: %w", result.ExitCode, ErrRunFailed)
	case waitErr != nil:
		return &result, fmt.Errorf("wait for container: %w: %w", ErrRunFailed, waitErr)
	}

	result.ExitCode = 0
	return &result, nil
}

// buildArgs assembles the docker run argument list.
func (d *DockerExecutor) buildArgs(spec RunSpec, runID string) []string {
	args := []string{"run", "--rm", "--interactive"}

	for key, value := range spec.Labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, "--label", RunLabel+"="+runID)

	network := spec.NetworkMode
	if network == "" {
		network = string(domainNetworkBridge)
	}
	args = append(args, "--network", network)

	for _, capability := range spec.Caps {
		args = append(args, "--cap-add", capability)
	}

	memory := spec.MemoryMB
	if memory <= 0 {
		memory = DefaultMemoryMB
	}
	args = append(args, "--memory", strconv.Itoa(memory)+"m")

	cpus := spec.CPUs
	if cpus <= 0 {
		cpus = DefaultCPUs
	}
	args = append(args, "--cpus", strconv.FormatFloat(cpus, 'f', 1, 64))

	pids := spec.PIDsLimit
	if pids <= 0 {
		pids = DefaultPIDsLimit
	}
	args = append(args, "--pids-limit", strconv.Itoa(pids))

	if spec.ReadOnly {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,exec,size=64m")
	}

	if d.workspace != "" {
		args = append(args, "--volume", d.workspace+":/work:rw")
	}

	workDir := spec.WorkDir
	if workDir == "" {
		workDir = "/work"
	}
	args = append(args, "--workdir", workDir)

	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}

	envKeys := make([]string, 0, len(spec.Env))
	for key := range spec.Env {
		envKeys = append(envKeys, key)
	}
	slices.Sort(envKeys)
	for _, key := range envKeys {
		args = append(args, "--env", key+"="+spec.Env[key])
	}

	args = append(args, spec.Image)
	args = append(args, spec.Argv...)

	return args
}

// domainNetworkBridge is the default container network mode.
const domainNetworkBridge = "bridge"

// killByLabel force-removes every container created by this run.
//
// The kill deliberately ignores the caller's cancellation: the run has already
// been cancelled, and the container must still be reaped. A fresh bounded context
// is the only way to guarantee the cleanup attempt survives.
func (d *DockerExecutor) killByLabel(parent context.Context, runID string) { //nolint:contextcheck // cleanup must outlive the cancelled parent
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), killGrace)
	defer cancel()

	filter := "label=" + RunLabel + "=" + runID

	list := exec.CommandContext(ctx, d.docker, "ps", "--quiet", "--filter", filter) // #nosec G204 -- filter derives from the run label
	output, err := list.Output()
	if err != nil {
		return
	}

	for _, id := range strings.Fields(string(output)) {
		kill := exec.CommandContext(ctx, d.docker, "kill", id) // #nosec G204 -- id comes from docker ps output
		if err := kill.Run(); err != nil {
			continue
		}
	}
}

// Reap removes containers left behind by a previous process.
func (d *DockerExecutor) Reap(ctx context.Context) (int, error) {
	filters := []string{
		"--filter", "label=" + RunLabel,
		"--filter", "status=exited",
	}

	list := exec.CommandContext(ctx, d.docker, append([]string{"ps", "--quiet", "--all"}, filters...)...) // #nosec G204 -- filters are literal label selectors
	output, err := list.Output()
	if err != nil {
		return 0, fmt.Errorf("list orphaned containers: %w: %w", ErrDockerUnavailable, err)
	}

	ids := strings.Fields(string(output))
	for _, id := range ids {
		remove := exec.CommandContext(ctx, d.docker, "rm", "--force", id) // #nosec G204 -- id comes from docker ps output
		if err := remove.Run(); err != nil {
			return 0, fmt.Errorf("remove container %s: %w: %w", id, ErrRunFailed, err)
		}
	}

	return len(ids), nil
}
