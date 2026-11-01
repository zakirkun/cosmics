package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// jsonSchema is the subset of JSON Schema the registry uses.
type jsonSchema struct {
	Type                 string                `json:"type"`
	Required             []string              `json:"required"`
	Properties           map[string]jsonSchema `json:"properties"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
	Enum                 []any                 `json:"enum"`
	Minimum              *float64              `json:"minimum"`
	Maximum              *float64              `json:"maximum"`
	MinLength            *int                  `json:"minLength"`
	MaxLength            *int                  `json:"maxLength"`
	Pattern              string                `json:"pattern"`
}

// validateAgainstSchema checks a decoded object against the tool's schema.
func validateAgainstSchema(rawSchema json.RawMessage, args map[string]any) error {
	var schema jsonSchema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return fmt.Errorf("tool schema is unreadable: %w", err)
	}

	var problems []error

	for _, name := range schema.Required {
		if _, present := args[name]; !present {
			problems = append(problems, fmt.Errorf("field %q is required", name))
		}
	}

	if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
		known := make(map[string]struct{}, len(schema.Properties))
		for name := range schema.Properties {
			known[name] = struct{}{}
		}

		unknown := make([]string, 0)
		for name := range args {
			if _, ok := known[name]; !ok {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)
		for _, name := range unknown {
			problems = append(problems, fmt.Errorf("field %q is not accepted by this tool", name))
		}
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value, present := args[name]
		if !present {
			continue
		}
		if err := validateValue(name, schema.Properties[name], value); err != nil {
			problems = append(problems, err)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidArgs, joinProblems(problems))
	}
	return nil
}

// validateValue checks one field value against its property schema.
func validateValue(path string, schema jsonSchema, value any) error {
	switch schema.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("field %q must be a string", path)
		}
		return validateString(path, schema, text)

	case "integer":
		number, ok := toFloat(value)
		if !ok {
			return fmt.Errorf("field %q must be an integer", path)
		}
		if number != float64(int64(number)) {
			return fmt.Errorf("field %q must be a whole number", path)
		}
		return validateNumber(path, schema, number)

	case "number":
		number, ok := toFloat(value)
		if !ok {
			return fmt.Errorf("field %q must be a number", path)
		}
		return validateNumber(path, schema, number)

	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q must be a boolean", path)
		}

	case "array", "object":
		return nil

	case "":
		return nil

	default:
		return fmt.Errorf("field %q has unsupported schema type %q", path, schema.Type)
	}

	return nil
}

// validateString enforces length, pattern, and enum constraints on a string.
func validateString(path string, schema jsonSchema, text string) error {
	if schema.MinLength != nil && len(text) < *schema.MinLength {
		return fmt.Errorf("field %q must be at least %d characters", path, *schema.MinLength)
	}
	if schema.MaxLength != nil && len(text) > *schema.MaxLength {
		return fmt.Errorf("field %q must be at most %d characters", path, *schema.MaxLength)
	}

	if schema.Pattern != "" {
		compiled, err := regexp.Compile(schema.Pattern)
		if err != nil {
			return fmt.Errorf("field %q has an unusable pattern in its schema: %w", path, err)
		}
		if !compiled.MatchString(text) {
			return fmt.Errorf("field %q value %q does not match the required format", path, text)
		}
	}

	if len(schema.Enum) > 0 {
		for _, allowed := range schema.Enum {
			if candidate, ok := allowed.(string); ok && candidate == text {
				return nil
			}
		}
		return fmt.Errorf("field %q value %q is not one of the permitted values", path, text)
	}

	return nil
}

// validateNumber enforces numeric bounds.
func validateNumber(path string, schema jsonSchema, number float64) error {
	if schema.Minimum != nil && number < *schema.Minimum {
		return fmt.Errorf("field %q must be at least %v", path, *schema.Minimum)
	}
	if schema.Maximum != nil && number > *schema.Maximum {
		return fmt.Errorf("field %q must be at most %v", path, *schema.Maximum)
	}
	return nil
}

// toFloat normalises the numeric types encoding/json produces.
func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	}
	return 0, false
}

// joinProblems renders multiple validation problems as one message.
func joinProblems(problems []error) error {
	messages := make([]string, 0, len(problems))
	for _, problem := range problems {
		messages = append(messages, problem.Error())
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}
