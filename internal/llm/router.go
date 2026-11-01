package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoRoutes is returned when a router was built with an empty chain.
var ErrNoRoutes = errors.New("llm: no route targets configured")

// RouteTarget is one link in a fallback chain: a concrete provider plus the
// model and token budget to use with it.
type RouteTarget struct {
	// Provider performs the request.
	Provider Provider

	// Name identifies the provider for logs and persisted call records.
	Name string

	// Model is the model identifier sent to the provider. For a gateway combo
	// this may resolve to a different model, which the Response reports.
	Model string

	// MaxTokens is the completion budget for this target.
	MaxTokens int

	// ReasoningEffort requests extended reasoning when the provider supports it.
	ReasoningEffort string
}

// Validate rejects a target missing a required field.
func (t RouteTarget) Validate() error {
	if t.Provider == nil {
		return fmt.Errorf("route target %q has no provider: %w", t.Name, ErrNoRoutes)
	}
	if t.Name == "" {
		return fmt.Errorf("route target has no name: %w", ErrNoRoutes)
	}
	if t.Model == "" {
		return fmt.Errorf("route target %q has no model: %w", t.Name, ErrNoRoutes)
	}
	return nil
}

// Router tries an ordered chain of targets and fails over on retryable errors.
//
// It is the only component that knows which model serves which stage, and it is
// the only place that decides to move down a chain. Providers stay unaware of
// each other.
type Router struct {
	targets  []RouteTarget
	recorder Recorder
	clock    Clock
}

// Clock supplies instants for call records without importing the domain package
// into every provider.
type Clock interface {
	Now() Timestamp
}

// Timestamp is Unix milliseconds.
type Timestamp int64

// RouterOptions configures a Router.
type RouterOptions struct {
	Targets  []RouteTarget
	Recorder Recorder
	Clock    Clock
}

// NewRouter validates the chain and returns a router.
func NewRouter(opts RouterOptions) (*Router, error) {
	if len(opts.Targets) == 0 {
		return nil, ErrNoRoutes
	}
	for i, target := range opts.Targets {
		if err := target.Validate(); err != nil {
			return nil, fmt.Errorf("route target %d: %w", i, err)
		}
	}
	return &Router{
		targets:  opts.Targets,
		recorder: opts.Recorder,
		clock:    opts.Clock,
	}, nil
}

// Attempt is one target's outcome, kept so the caller can report which models
// were tried and why each failed.
type Attempt struct {
	TargetIndex  int
	ProviderName string
	Model        string
	Err          error
	Retryable    bool
	Latency      time.Duration
}

// RouteResult is the outcome of a whole chain walk.
type RouteResult struct {
	Response *Response
	Attempts []Attempt
}

// Complete walks the chain until a target succeeds.
//
// Failover happens only on errors the provider classified as retryable. A
// non-retryable error (malformed request, unknown model, auth failure) stops the
// walk immediately: retrying the same broken request against a different model
// would hide a caller bug.
func (r *Router) Complete(ctx context.Context, req Request) (*RouteResult, error) {
	return r.walk(ctx, req, nil)
}

// Stream walks the chain, streaming from the first target that connects.
//
// A target that produces chunks and then fails is NOT failed over, because the
// sink has already observed partial output and replaying would duplicate it.
// Failover is only possible before the first chunk.
func (r *Router) Stream(ctx context.Context, req Request, sink ChunkSink) (*RouteResult, error) {
	if sink == nil {
		return nil, fmt.Errorf("router stream: nil sink: %w", ErrNotRetryable)
	}
	return r.walk(ctx, req, sink)
}

// walk is the shared chain traversal.
func (r *Router) walk(ctx context.Context, req Request, sink ChunkSink) (*RouteResult, error) {
	attempts := make([]Attempt, 0, len(r.targets))

	for i, target := range r.targets {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("router: cancelled after %d attempt(s): %w", i, err)
		}

		resp, err := r.invoke(ctx, target, req, sink)
		if err != nil {
			attempt := Attempt{
				TargetIndex:  i,
				ProviderName: target.Name,
				Model:        target.Model,
				Err:          err,
				Retryable:    errors.Is(err, ErrRetryable),
			}
			attempts = append(attempts, attempt)

			if !attempt.Retryable {
				return &RouteResult{Attempts: attempts},
					fmt.Errorf("router: target %s/%s failed permanently: %w", target.Name, target.Model, err)
			}
			continue
		}

		attempts = append(attempts, Attempt{
			TargetIndex:  i,
			ProviderName: target.Name,
			Model:        target.Model,
			Latency:      resp.Latency,
		})
		resp.Attempts = len(attempts)
		return &RouteResult{Response: resp, Attempts: attempts}, nil
	}

	return &RouteResult{Attempts: attempts}, fmt.Errorf(
		"router: all %d target(s) failed: %w", len(attempts), ErrChainExhausted)
}

// invoke sends one request to one target and records the outcome.
func (r *Router) invoke(ctx context.Context, target RouteTarget, req Request, sink ChunkSink) (*Response, error) {
	targetReq := req
	targetReq.Model = target.Model
	if target.MaxTokens > 0 {
		targetReq.MaxTokens = target.MaxTokens
	}

	started := r.now()
	startedWall := time.Now()

	resp, err := r.dispatch(ctx, target, targetReq, sink)

	finished := r.now()
	latency := time.Since(startedWall)

	if resp != nil {
		resp.Latency = latency
	}

	r.record(ctx, target, targetReq, resp, err, started, finished, latency, sink != nil)

	if err != nil {
		return nil, err
	}
	return resp, nil
}

// dispatch performs the provider call for the given mode.
func (r *Router) dispatch(ctx context.Context, target RouteTarget, req Request, sink ChunkSink) (*Response, error) {
	if sink != nil {
		return target.Provider.Stream(ctx, req, sink)
	}
	return target.Provider.Complete(ctx, req)
}

// now returns the current instant from the injected clock.
func (r *Router) now() Timestamp {
	if r.clock == nil {
		return Timestamp(time.Now().UTC().UnixMilli())
	}
	return r.clock.Now()
}

// record persists one call attempt when a recorder is configured.
//
// Recording never fails the call: losing an audit row is bad, but failing a
// successful model response because the ledger write failed is worse. The error
// is attached to the response instead.
func (r *Router) record(
	ctx context.Context,
	target RouteTarget,
	req Request,
	resp *Response,
	callErr error,
	started, finished Timestamp,
	latency time.Duration,
	streamed bool,
) {
	if r.recorder == nil {
		return
	}

	stage, _ := StageFrom(ctx)
	purpose, _ := PurposeFrom(ctx)

	rec := CallRecord{
		Stage:          stage,
		Provider:       target.Provider.Kind(),
		ModelRequested: target.Model,
		Purpose:        purpose,
		Latency:        latency,
		Streamed:       streamed,
		StartedAt:      domainTimestamp(started),
		FinishedAt:     domainTimestamp(finished),
	}

	if resp != nil {
		rec.ModelServed = resp.ModelServed
		rec.Usage = resp.Usage
	}
	if callErr != nil {
		rec.Status = "error"
		rec.ErrorMessage = callErr.Error()
	} else {
		rec.Status = "ok"
	}

	// The provider has already answered, so a ledger failure must not turn a
	// successful call into an error. The record is attempted once and dropped;
	// the engagement budget is the authority on spend, not this row.
	_ = r.recorder.Record(ctx, rec, "", "")
}
