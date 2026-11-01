// Package agent implements the bounded ReAct loop that drives one stage.
//
// The loop is deterministic in structure: it validates every proposed action,
// executes read-only tools concurrently, applies state changes in order, and
// lets only the stage's exit predicate end the stage.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
)

// Sentinel errors describing why a loop stopped.
var (
	ErrIterationCap    = errors.New("agent: iteration cap reached")
	ErrToolLoop        = errors.New("agent: too many consecutive tool errors")
	ErrBudgetExhausted = errors.New("agent: budget exhausted")
	ErrUnknownAction   = errors.New("agent: unknown action")
	ErrInvalidAction   = errors.New("agent: invalid action")
)

// Limits bounds one stage body.
type Limits struct {
	MaxIterations   int
	MaxToolParallel int
	MaxToolErrors   int
}

// DefaultLimits returns the constants documented in CORE.md section 4.1.
func DefaultLimits() Limits {
	return Limits{
		MaxIterations:   40,
		MaxToolParallel: 4,
		MaxToolErrors:   5,
	}
}

// Validate rejects a nonsensical limit set.
func (l Limits) Validate() error {
	if l.MaxIterations <= 0 {
		return fmt.Errorf("max iterations %d must be positive: %w", l.MaxIterations, ErrInvalidAction)
	}
	if l.MaxToolParallel <= 0 {
		return fmt.Errorf("max tool parallelism %d must be positive: %w", l.MaxToolParallel, ErrInvalidAction)
	}
	if l.MaxToolErrors <= 0 {
		return fmt.Errorf("max tool errors %d must be positive: %w", l.MaxToolErrors, ErrInvalidAction)
	}
	return nil
}

// ActionKind enumerates what the model asked for.
type ActionKind string

// Action kinds. Every proposed tool call resolves to exactly one.
const (
	KindToolCall    ActionKind = "tool_call"
	KindSetFact     ActionKind = "set_fact"
	KindAddFinding  ActionKind = "add_finding"
	KindLoadSkill   ActionKind = "load_skill"
	KindAskOperator ActionKind = "ask_operator"
	KindFinishStage ActionKind = "finish_stage"
)

// Valid reports whether the kind is declared.
func (k ActionKind) Valid() bool {
	switch k {
	case KindToolCall, KindSetFact, KindAddFinding, KindLoadSkill, KindAskOperator, KindFinishStage:
		return true
	}
	return false
}

// Meta reports whether the action mutates state rather than running a tool.
func (k ActionKind) Meta() bool {
	switch k {
	case KindSetFact, KindAddFinding, KindLoadSkill, KindAskOperator, KindFinishStage:
		return true
	case KindToolCall:
		return false
	}
	return false
}

// Action is one validated proposal from the model.
type Action struct {
	Kind ActionKind

	// ToolID and Args are set for KindToolCall.
	ToolID string
	Args   json.RawMessage
	Target string

	// Subject, Predicate, Value are set for KindSetFact.
	Subject   string
	Predicate string
	Value     string

	// Title and Severity are set for KindAddFinding.
	Title    string
	Severity string

	// SkillName is set for KindLoadSkill.
	SkillName string

	// Question is set for KindAskOperator.
	Question string

	// CallID correlates the result back to the model's tool_call id.
	CallID string
}

// Result is the outcome of executing one action.
type Result struct {
	Action Action

	// Output is what the model is told, already truncated by the caller.
	Output string

	// Err is non-nil when the action failed.
	Err error

	// ExitMet is meaningful only for KindFinishStage.
	ExitMet bool

	// ExitReason explains an unmet exit predicate.
	ExitReason string
}

// ContextString renders the result for the tool message.
func (r Result) ContextString() string {
	if r.Err != nil {
		return "error: " + r.Err.Error()
	}
	if r.Action.Kind == KindFinishStage && !r.ExitMet {
		return "exit criteria not met: " + r.ExitReason
	}
	return r.Output
}

// ToolInvoker runs one registered tool. The agent never knows how a tool is
// executed; the composition root supplies a Docker-backed implementation and
// tests supply a fake.
type ToolInvoker interface {
	Invoke(ctx context.Context, toolID string, args json.RawMessage, target string) (string, error)
}

// StateWriter persists the meta-actions the model can request.
type StateWriter interface {
	SetFact(ctx context.Context, subject, predicate, value string) error
	AddFinding(ctx context.Context, title, severity string) (domain.ID, error)
	LoadSkill(ctx context.Context, name string) (string, error)
	AskOperator(ctx context.Context, question string) (string, error)
}

// ExitPredicate decides whether a stage is finished. It reads persisted state.
type ExitPredicate interface {
	Evaluate(ctx context.Context) (met bool, reason string, err error)
}

// Deps is everything the loop needs.
type Deps struct {
	Model     Model
	Tools     ToolInvoker
	State     StateWriter
	Exit      ExitPredicate
	Limits    Limits
	OnMessage func(Message)
}

// Message is a progress notification for the UI. The loop never blocks on it.
type Message struct {
	Iteration int
	Kind      string
	Text      string
}

// Model is the narrow model surface the loop depends on.
type Model interface {
	Complete(ctx context.Context, req llm.Request) (*llm.RouteResult, error)
}

// Loop drives one stage body.
type Loop struct {
	deps Deps
}

// New validates the dependencies and returns a loop.
func New(deps Deps) (*Loop, error) {
	if deps.Model == nil {
		return nil, fmt.Errorf("model: %w", ErrInvalidAction)
	}
	if deps.Exit == nil {
		return nil, fmt.Errorf("exit predicate: %w", ErrInvalidAction)
	}
	if err := deps.Limits.Validate(); err != nil {
		return nil, err
	}
	return &Loop{deps: deps}, nil
}

// Run executes the loop with the given conversation and system prompt.
//
// It returns nil only when the exit predicate agreed. A plain text reply never
// ends a stage: the model is nudged back to the contract, because a chatty model
// declaring victory is the most common failure mode of this design.
func (l *Loop) Run(ctx context.Context, system string, messages []llm.Message) error {
	conversation := messages
	consecutiveErrors := 0

	for iteration := 1; iteration <= l.deps.Limits.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent loop: %w", err)
		}

		route, err := l.deps.Model.Complete(ctx, llm.Request{
			Messages: conversation,
			System:   system,
		})
		if err != nil {
			return fmt.Errorf("agent loop iteration %d: %w", iteration, err)
		}
		if route == nil || route.Response == nil {
			return fmt.Errorf("agent loop iteration %d: empty response: %w", iteration, ErrInvalidAction)
		}

		reply := route.Response.Message
		conversation = append(conversation, reply)
		l.notify(Message{Iteration: iteration, Kind: "assistant", Text: reply.Content})

		if len(reply.ToolCalls) == 0 {
			conversation = append(conversation, llm.Message{
				Role:    llm.RoleUser,
				Content: nudgePrompt,
			})
			continue
		}

		outcomes := l.execute(ctx, reply.ToolCalls)

		for _, outcome := range outcomes {
			conversation = append(conversation, llm.Message{
				Role:    llm.RoleTool,
				ToolID:  outcome.Action.CallID,
				Content: outcome.ContextString(),
			})

			if outcome.Err != nil {
				consecutiveErrors++
				if consecutiveErrors >= l.deps.Limits.MaxToolErrors {
					return fmt.Errorf("agent loop: %d consecutive tool errors: %w", consecutiveErrors, ErrToolLoop)
				}
				continue
			}
			consecutiveErrors = 0

			if outcome.Action.Kind == KindFinishStage && outcome.ExitMet {
				return nil
			}
		}
	}

	return fmt.Errorf("agent loop exceeded %d iterations: %w", l.deps.Limits.MaxIterations, ErrIterationCap)
}

// nudgePrompt returns the model to the stage contract after a plain text reply.
const nudgePrompt = "Continue the stage. Use a tool, or call finish_stage. A plain reply does not end the stage."

// notify reports progress without letting a listener failure stop the loop.
func (l *Loop) notify(msg Message) {
	if l.deps.OnMessage == nil {
		return
	}
	l.deps.OnMessage(msg)
}
