package exec

import (
	"context"
	"sync"
)

// Fake is an in-memory executor for pipeline tests.
//
// It records every spec it was given and replays a configured outcome. It is not
// a Docker substitute: tests that must prove real container behaviour use the
// docker-tagged suite instead.
type Fake struct {
	mu sync.Mutex

	specs   []RunSpec
	results map[string]*Result
	errors  map[string]error
	timeout bool
	images  map[string]bool

	onRun func(spec RunSpec)
}

// NewFake returns an empty fake executor.
func NewFake() *Fake {
	return &Fake{
		results: make(map[string]*Result),
		errors:  make(map[string]error),
		images:  make(map[string]bool),
	}
}

// SetResult configures the outcome for a tool identifier.
func (f *Fake) SetResult(toolID string, result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[toolID] = &result
}

// SetError configures a failure for a tool identifier.
func (f *Fake) SetError(toolID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[toolID] = err
}

// SetTimeout makes every future run report a timeout.
func (f *Fake) SetTimeout(timedOut bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timeout = timedOut
}

// OnRun registers a hook invoked at the start of every run.
func (f *Fake) OnRun(hook func(spec RunSpec)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRun = hook
}

// Specs returns every spec recorded so far.
func (f *Fake) Specs() []RunSpec {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]RunSpec, len(f.specs))
	copy(out, f.specs)
	return out
}

// Key returns the tool identifier a spec was recorded under.
func (f *Fake) Key(index int) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if index < 0 || index >= len(f.specs) {
		return ""
	}
	if len(f.specs[index].Argv) == 0 {
		return ""
	}
	return f.specs[index].Argv[0]
}

// Run records the spec and returns the configured outcome.
func (f *Fake) Run(ctx context.Context, spec RunSpec, sink StreamSink) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.specs = append(f.specs, spec)
	hook := f.onRun
	timedOut := f.timeout
	f.mu.Unlock()

	if hook != nil {
		hook(spec)
	}

	key := ""
	if len(spec.Argv) > 0 {
		key = spec.Argv[0]
	}

	f.mu.Lock()
	configuredResult := f.results[key]
	configuredErr := f.errors[key]
	f.mu.Unlock()

	if configuredResult != nil && sink != nil {
		for _, line := range splitOutput(configuredResult.Stdout) {
			if err := sink(StreamEvent{Stream: StreamStdout, Line: line}); err != nil {
				return nil, err
			}
		}
	}

	if configuredResult != nil {
		result := *configuredResult
		result.TimedOut = timedOut
		return &result, configuredErr
	}
	if configuredErr != nil {
		return nil, configuredErr
	}

	return &Result{ExitCode: 0, TimedOut: timedOut}, nil
}

// splitOutput splits captured output into lines for replay.
func splitOutput(text string) []string {
	var out []string
	current := ""
	for _, r := range text {
		if r == '\n' {
			out = append(out, current)
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

// EnsureImage records the image as present.
func (f *Fake) EnsureImage(_ context.Context, image string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[image] = true
	return nil
}

// Ping always succeeds.
func (f *Fake) Ping(context.Context) error { return nil }

// Reap always reports nothing to remove.
func (f *Fake) Reap(context.Context) (int, error) { return 0, nil }

// ImagePresent reports whether EnsureImage was called for an image.
func (f *Fake) ImagePresent(image string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[image]
}

// StaticExecutor returns one fixed result for every run, used by pipeline tests
// that do not care about per-tool outcomes.
type StaticExecutor struct {
	Result Result
	Err    error
	mu     sync.Mutex
	runs   int
}

// NewStatic returns an executor that always answers the same way.
func NewStatic(result Result) *StaticExecutor {
	return &StaticExecutor{Result: result}
}

// Run returns the fixed result.
func (s *StaticExecutor) Run(_ context.Context, spec RunSpec, sink StreamSink) (*Result, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.runs++
	s.mu.Unlock()

	if sink != nil && s.Result.Stdout != "" {
		_ = sink(StreamEvent{Stream: StreamStdout, Line: s.Result.Stdout})
	}
	result := s.Result
	return &result, s.Err
}

// EnsureImage does nothing.
func (s *StaticExecutor) EnsureImage(context.Context, string) error { return nil }

// Ping always succeeds.
func (s *StaticExecutor) Ping(context.Context) error { return nil }

// Reap reports nothing to remove.
func (s *StaticExecutor) Reap(context.Context) (int, error) { return 0, nil }

// Runs reports how many runs happened.
func (s *StaticExecutor) Runs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}
