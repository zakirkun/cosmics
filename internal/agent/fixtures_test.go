package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
)

// scriptedModel replays a fixed sequence of responses.
type scriptedModel struct {
	mu        sync.Mutex
	responses []llm.Response
	requests  []llm.Request
	err       error
}

func (m *scriptedModel) Complete(_ context.Context, req llm.Request) (*llm.RouteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	if len(m.responses) == 0 {
		return nil, errors.New("scripted model ran out of responses")
	}
	next := m.responses[0]
	m.responses = m.responses[1:]
	return &llm.RouteResult{Response: &next}, nil
}

// callCount reports how many completions were requested.
func (m *scriptedModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// recordingTools records invocations and returns configured output.
//
// Known lists the tool ids this fake honours. A name outside it fails exactly
// the way the real registry does, which is what makes a misnamed call testable.
type recordingTools struct {
	mu      sync.Mutex
	seen    []string
	output  string
	err     error
	known   map[string]bool
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (t *recordingTools) Invoke(_ context.Context, toolID string, _ json.RawMessage, _ string) (string, error) {
	if t.entered != nil {
		t.once.Do(func() { close(t.entered) })
	}
	if t.block != nil {
		<-t.block
	}

	t.mu.Lock()
	t.seen = append(t.seen, toolID)
	t.mu.Unlock()

	if t.known != nil && !t.known[toolID] {
		return "", errors.New("tool " + toolID + ": unknown tool")
	}
	if t.err != nil {
		return "", t.err
	}
	return t.output, nil
}

// seenCount reports how many invocations happened.
func (t *recordingTools) seenCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.seen)
}

// stubState records meta-action calls.
type stubState struct {
	facts    []string
	findings int
	skills   []string
	answers  string
}

func (s *stubState) SetFact(_ context.Context, subject, predicate, value string) error {
	s.facts = append(s.facts, subject+"/"+predicate+"="+value)
	return nil
}

func (s *stubState) AddFinding(_ context.Context, _, _ string) (domain.ID, error) {
	s.findings++
	return domain.NewULIDGenerator().New(), nil
}

func (s *stubState) LoadSkill(_ context.Context, name string) (string, error) {
	s.skills = append(s.skills, name)
	return "skill body for " + name, nil
}

func (s *stubState) AskOperator(_ context.Context, _ string) (string, error) {
	return s.answers, nil
}

// fixedExit reports a constant verdict.
type fixedExit struct {
	met    bool
	reason string
	err    error
	calls  int
	mu     sync.Mutex
}

func (e *fixedExit) Evaluate(context.Context) (bool, string, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return e.met, e.reason, e.err
}

// toolCallBody builds an assistant turn requesting one tool.
func toolCallBody(id, name, args string) llm.Response {
	return llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:        id,
				Name:      name,
				Arguments: json.RawMessage(args),
			}},
		},
		FinishReason: llm.FinishTools,
	}
}

// plainReply builds an assistant turn with no tool calls.
func plainReply(text string) llm.Response {
	return llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: text},
		FinishReason: llm.FinishStop,
	}
}
