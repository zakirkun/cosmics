package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cosmics/cosmics/internal/llm"
)

// execute resolves and runs one batch of tool calls.
//
// Two rules shape this function, both established from the live gateway:
//
//  1. The gateway returns several tool calls in ONE response, so independent
//     read-only work must run concurrently or the model's parallelism is wasted.
//  2. Meta-actions write state that a tool call in the same batch may produce,
//     so they run after the tool calls rather than beside them.
//
// Validation happens for the whole batch before anything executes: one malformed
// call must not prevent its siblings from running.
func (l *Loop) execute(ctx context.Context, calls []llm.ToolCall) []Result {
	actions := make([]Action, len(calls))
	validationErrs := make([]error, len(calls))

	for i, call := range calls {
		action, err := classify(call, "")
		if err != nil {
			validationErrs[i] = err
			continue
		}
		actions[i] = action
	}

	results := make([]Result, len(calls))
	for i := range calls {
		if validationErrs[i] != nil {
			results[i] = Result{
				Action: Action{CallID: calls[i].ID},
				Err:    validationErrs[i],
			}
		}
	}

	l.runTools(ctx, calls, actions, results)
	l.runMeta(ctx, calls, actions, results)

	return results
}

// runTools executes the tool-call actions concurrently, bounded by the limit.
func (l *Loop) runTools(ctx context.Context, _ []llm.ToolCall, actions []Action, results []Result) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		limitCh = make(chan struct{}, l.deps.Limits.MaxToolParallel)
	)

	for i := range actions {
		if results[i].Err != nil || actions[i].Kind != KindToolCall {
			continue
		}

		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			select {
			case limitCh <- struct{}{}:
				defer func() { <-limitCh }()
			case <-ctx.Done():
				mu.Lock()
				results[index] = Result{Action: actions[index], Err: ctx.Err()}
				mu.Unlock()
				return
			}

			output, err := l.invokeTool(ctx, actions[index])

			mu.Lock()
			results[index] = Result{Action: actions[index], Output: output, Err: err}
			mu.Unlock()
		}(i)
	}

	wg.Wait()
}

// invokeTool runs one tool through the injected invoker.
//
// The output is truncated before it reaches the conversation: an unbounded tool
// result would consume the whole context window and evict the stage contract.
func (l *Loop) invokeTool(ctx context.Context, action Action) (string, error) {
	if l.deps.Tools == nil {
		return "", fmt.Errorf("tool %s: no invoker configured: %w", action.ToolID, ErrUnknownAction)
	}

	output, err := l.deps.Tools.Invoke(ctx, action.ToolID, action.Args, action.Target)
	if err != nil {
		return "", err
	}
	return truncateOutput(output, maxToolOutputBytes), nil
}

// runMeta applies state-changing actions in the order the model emitted them.
func (l *Loop) runMeta(ctx context.Context, _ []llm.ToolCall, actions []Action, results []Result) {
	for i := range actions {
		if results[i].Err != nil || !actions[i].Kind.Meta() {
			continue
		}
		results[i] = l.applyMeta(ctx, actions[i])
	}
}

// applyMeta performs one meta-action.
func (l *Loop) applyMeta(ctx context.Context, action Action) Result {
	result := Result{Action: action}

	switch action.Kind {
	case KindSetFact:
		if l.deps.State == nil {
			result.Err = fmt.Errorf("set_fact: no state writer: %w", ErrUnknownAction)
			return result
		}
		if err := l.deps.State.SetFact(ctx, action.Subject, action.Predicate, action.Value); err != nil {
			result.Err = fmt.Errorf("set_fact %s/%s: %w", action.Subject, action.Predicate, err)
			return result
		}
		result.Output = "ok"

	case KindAddFinding:
		if l.deps.State == nil {
			result.Err = fmt.Errorf("add_finding: no state writer: %w", ErrUnknownAction)
			return result
		}
		id, err := l.deps.State.AddFinding(ctx, action.Title, action.Severity)
		if err != nil {
			result.Err = fmt.Errorf("add_finding %q: %w", action.Title, err)
			return result
		}
		result.Output = "finding " + id.String()

	case KindLoadSkill:
		if l.deps.State == nil {
			result.Err = fmt.Errorf("load_skill: no state writer: %w", ErrUnknownAction)
			return result
		}
		body, err := l.deps.State.LoadSkill(ctx, action.SkillName)
		if err != nil {
			result.Err = fmt.Errorf("load_skill %q: %w", action.SkillName, err)
			return result
		}
		result.Output = body

	case KindAskOperator:
		if l.deps.State == nil {
			result.Err = fmt.Errorf("ask_operator: no state writer: %w", ErrUnknownAction)
			return result
		}
		answer, err := l.deps.State.AskOperator(ctx, action.Question)
		if err != nil {
			result.Err = fmt.Errorf("ask_operator: %w", err)
			return result
		}
		result.Output = answer

	case KindFinishStage:
		if l.deps.Exit == nil {
			result.Err = fmt.Errorf("finish_stage: no exit predicate: %w", ErrUnknownAction)
			return result
		}
		met, reason, err := l.deps.Exit.Evaluate(ctx)
		if err != nil {
			result.Err = fmt.Errorf("evaluate exit criteria: %w", err)
			return result
		}
		result.ExitMet = met
		result.ExitReason = reason
		if met {
			result.Output = "stage complete"
		}

	case KindToolCall:
		result.Err = fmt.Errorf("tool calls are executed by runTools: %w", ErrInvalidAction)
	}

	return result
}

// ExecuteBatch is exported for the pipeline, which owns the conversation and
// only needs the batch semantics. It reuses the unexported implementation so the
// two callers cannot drift.
func (l *Loop) ExecuteBatch(ctx context.Context, calls []llm.ToolCall) []Result {
	return l.execute(ctx, calls)
}

// JoinResults combines independent errors from a batch.
func JoinResults(results []Result) error {
	var errs []error
	for _, result := range results {
		if result.Err != nil {
			errs = append(errs, result.Err)
		}
	}
	return errors.Join(errs...)
}
