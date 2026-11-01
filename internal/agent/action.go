package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
)

// Meta-action tool names the model is allowed to call directly. They are
// resolved to Action kinds here rather than being sent to the tool invoker.
const (
	metaSetFact     = "set_fact"
	metaAddFinding  = "add_finding"
	metaLoadSkill   = "load_skill"
	metaAskOperator = "ask_operator"
	metaFinishStage = "finish_stage"
)

// classify resolves one provider tool call into a validated Action.
func classify(call llm.ToolCall, stage domain.StageKind) (Action, error) {
	if call.Name == "" {
		return Action{}, fmt.Errorf("tool call %s has no name: %w", call.ID, ErrInvalidAction)
	}

	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if !json.Valid(args) {
		return Action{}, fmt.Errorf("tool call %s arguments are not JSON: %w", call.ID, ErrInvalidAction)
	}

	switch call.Name {
	case metaFinishStage:
		return Action{Kind: KindFinishStage, CallID: call.ID}, nil

	case metaSetFact:
		var payload setFactArgs
		if err := json.Unmarshal(args, &payload); err != nil {
			return Action{}, fmt.Errorf("set_fact arguments: %w", err)
		}
		if payload.Subject == "" || payload.Predicate == "" {
			return Action{}, fmt.Errorf("set_fact requires subject and predicate: %w", ErrInvalidAction)
		}
		return Action{
			Kind: KindSetFact, CallID: call.ID,
			Subject: payload.Subject, Predicate: payload.Predicate, Value: payload.Value,
		}, nil

	case metaAddFinding:
		var payload addFindingArgs
		if err := json.Unmarshal(args, &payload); err != nil {
			return Action{}, fmt.Errorf("add_finding arguments: %w", err)
		}
		if payload.Title == "" {
			return Action{}, fmt.Errorf("add_finding requires a title: %w", ErrInvalidAction)
		}
		return Action{
			Kind: KindAddFinding, CallID: call.ID,
			Title: payload.Title, Severity: payload.Severity,
		}, nil

	case metaLoadSkill:
		var payload loadSkillArgs
		if err := json.Unmarshal(args, &payload); err != nil {
			return Action{}, fmt.Errorf("load_skill arguments: %w", err)
		}
		if payload.Name == "" {
			return Action{}, fmt.Errorf("load_skill requires a name: %w", ErrInvalidAction)
		}
		return Action{Kind: KindLoadSkill, CallID: call.ID, SkillName: payload.Name}, nil

	case metaAskOperator:
		var payload askOperatorArgs
		if err := json.Unmarshal(args, &payload); err != nil {
			return Action{}, fmt.Errorf("ask_operator arguments: %w", err)
		}
		return Action{Kind: KindAskOperator, CallID: call.ID, Question: payload.Question}, nil

	default:
		var envelope toolEnvelope
		if err := json.Unmarshal(args, &envelope); err != nil {
			return Action{}, fmt.Errorf("tool call %s arguments: %w", call.ID, err)
		}
		if stage != "" && envelope.Stage != "" && envelope.Stage != string(stage) {
			return Action{}, fmt.Errorf("tool call %s targets stage %q while running %q: %w",
				call.ID, envelope.Stage, stage, ErrInvalidAction)
		}
		return Action{
			Kind:   KindToolCall,
			ToolID: call.Name,
			Args:   args,
			Target: envelope.Target,
			CallID: call.ID,
		}, nil
	}
}

type toolEnvelope struct {
	Target string `json:"target"`
	Stage  string `json:"stage"`
}

type setFactArgs struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Value     string `json:"value"`
}

type addFindingArgs struct {
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

type askOperatorArgs struct {
	Question string `json:"question"`
}

// truncateOutput bounds a tool result before it enters the conversation.
//
// A result that would flood the context is cut with an explicit marker so the
// model knows it is reading a fragment rather than the whole output.
func truncateOutput(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	cut := text[:maxBytes]
	if index := strings.LastIndexByte(cut, '\n'); index > 0 {
		cut = cut[:index]
	}
	return cut + fmt.Sprintf("\n… (truncated at %d of %d bytes)", len(cut), len(text))
}
