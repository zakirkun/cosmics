package tools

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// unquotedAction matches a value substitution that is not wrapped in q.
//
// Every argument value must pass through q so the tokeniser receives an explicit
// quoted string. A bare {{.field}} would be split on whitespace, which is how a
// data value silently becomes an extra command-line flag.
var unquotedAction = regexp.MustCompile(`\{\{\s*(\.[A-Za-z0-9_]+)\s*\}\}`)

// validateCommandTemplate parses a template and rejects an unquoted substitution.
func validateCommandTemplate(source string) error {
	if _, err := template.New("validate").Option("missingkey=zero").Funcs(template.FuncMap{
		"q": quoteForArgv,
	}).Parse(source); err != nil {
		return fmt.Errorf("command_template does not parse: %w", err)
	}

	if match := unquotedAction.FindString(source); match != "" {
		return fmt.Errorf("command_template substitutes %s without quoting; wrap it as {{q .field}}", match)
	}

	return nil
}

// TemplateReferences returns every argument name a template substitutes.
func TemplateReferences(source string) ([]string, error) {
	parsed, err := template.New("refs").Funcs(template.FuncMap{
		"q": quoteForArgv,
	}).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("command_template does not parse: %w", err)
	}

	seen := make(map[string]struct{})
	var names []string

	for _, node := range parsed.Tree.Root.Nodes {
		collectFieldNodes(node, seen, &names)
	}

	return names, nil
}

// collectFieldNodes walks a template node tree collecting field references.
func collectFieldNodes(node any, seen map[string]struct{}, names *[]string) {
	field, ok := node.(interface {
		String() string
	})
	if !ok {
		return
	}

	text := field.String()
	for _, match := range regexp.MustCompile(`\.[A-Za-z0-9_]+`).FindAllString(text, -1) {
		name := strings.TrimPrefix(match, ".")
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		*names = append(*names, name)
	}
}
