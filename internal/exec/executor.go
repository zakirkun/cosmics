// Package exec runs tools inside Docker containers and streams their output.
//
// It shells out to the docker CLI rather than linking the Docker SDK: the CLI is
// what the operator has installed and can debug, and a container that outlives a
// killed CLI is reaped explicitly rather than assumed away.
package exec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors for executor failures.
var (
	ErrDockerUnavailable = errors.New("exec: docker is not available")
	ErrImageMissing      = errors.New("exec: image is not present")
	ErrToolBinaryMissing = errors.New("exec: tool binary is not present in the image")
	ErrRunFailed         = errors.New("exec: run failed")
)

// Stream names one output channel.
type Stream string

// Output streams.
const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

// StreamEvent is one line of live output.
type StreamEvent struct {
	Stream Stream
	Line   string
	At     domain.TimestampMs
}

// StreamSink receives live output. Returning an error aborts the run.
type StreamSink func(StreamEvent) error

// RunSpec describes one container execution.
type RunSpec struct {
	Image       string
	Argv        []string
	Env         map[string]string
	WorkDir     string
	NetworkMode string
	Caps        []string
	Timeout     time.Duration
	Labels      map[string]string
	ReadOnly    bool
	User        string
	MemoryMB    int
	CPUs        float64
	PIDsLimit   int
}

// Validate rejects a spec that cannot produce a usable container.
func (s RunSpec) Validate() error {
	if s.Image == "" {
		return fmt.Errorf("%w: image is empty", ErrRunFailed)
	}
	if strings.HasPrefix(s.Image, "-") {
		return fmt.Errorf("%w: image %q must not start with '-', which docker would parse as a flag", ErrRunFailed, s.Image)
	}
	if strings.ContainsAny(s.Image, " \t\n\r") {
		return fmt.Errorf("%w: image %q must not contain whitespace", ErrRunFailed, s.Image)
	}
	if len(s.Argv) == 0 {
		return fmt.Errorf("%w: argv is empty", ErrRunFailed)
	}
	if s.Timeout < 0 {
		return fmt.Errorf("%w: timeout must not be negative", ErrRunFailed)
	}
	switch s.NetworkMode {
	case "", "bridge", "host", "none":
	default:
		return fmt.Errorf("%w: network mode %q is not supported", ErrRunFailed, s.NetworkMode)
	}
	return nil
}

// Result is the outcome of one run.
type Result struct {
	ExitCode     int
	Stdout       string
	Stderr       string
	StdoutSHA256 string
	StderrSHA256 string
	StartedAt    domain.TimestampMs
	FinishedAt   domain.TimestampMs
	Duration     time.Duration
	TimedOut     bool
	ContainerID  string
	DroppedLines int
}

// Succeeded reports whether the process exited cleanly.
func (r Result) Succeeded() bool { return r.ExitCode == 0 && !r.TimedOut }

// Size reports the captured stdout length.
func (r Result) Size() int { return len(r.Stdout) }

// Executor runs tools and manages their images.
type Executor interface {
	Run(ctx context.Context, spec RunSpec, sink StreamSink) (*Result, error)
	EnsureImage(ctx context.Context, image string) error
	Ping(ctx context.Context) error
	Reap(ctx context.Context) (int, error)
}

// digestOf returns the hex SHA-256 of a string.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
