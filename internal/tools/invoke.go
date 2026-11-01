package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Invocation is one fully validated tool run, ready to hand to an executor.
type Invocation struct {
	Tool   Tool
	Args   map[string]any
	Target string
}

// BuildInvocation validates raw model-supplied arguments against the tool's
// schema and returns an invocation.
//
// Arguments cross a trust boundary here: they were produced by a language model
// and are untrusted. Everything is validated before an argv exists, so a malformed
// or hostile value never reaches the executor.
func BuildInvocation(reg *Registry, toolID string, rawArgs json.RawMessage) (Invocation, error) {
	tool, err := reg.Get(toolID)
	if err != nil {
		return Invocation{}, err
	}
	if !tool.Enabled {
		return Invocation{}, fmt.Errorf("tool %q: %w", toolID, ErrToolDisabled)
	}

	decoded, err := decodeArgs(rawArgs)
	if err != nil {
		return Invocation{}, fmt.Errorf("tool %q: %w", toolID, err)
	}

	if err := validateAgainstSchema(tool.ArgSchema, decoded); err != nil {
		return Invocation{}, fmt.Errorf("tool %q: %w", toolID, err)
	}

	target, _ := decoded["target"].(string)

	return Invocation{Tool: tool, Args: decoded, Target: target}, nil
}

// decodeArgs parses the raw argument object into a generic map.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON object: %w: %w", ErrInvalidArgs, err)
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}
