package tools

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

// Argv renders the command template into a token slice.
//
// Rendering produces a string, then the string is tokenised with quote handling.
// No shell is ever involved: the tokens are passed to exec directly, so a value
// containing a semicolon stays inside one argument instead of starting a command.
func (i Invocation) Argv() ([]string, error) {
	rendered, err := i.render()
	if err != nil {
		return nil, err
	}

	tokens, err := splitArgs(rendered)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("template rendered no command: %w", ErrInvalidArgv)
	}

	return tokens, nil
}

// render executes the command template against the validated arguments.
//
// Every substituted value is quoted before it reaches the tokeniser. Without this
// a value containing whitespace would split into several argv elements, which is
// how an argument becomes an unintended flag or command.
func (i Invocation) render() (string, error) {
	tmpl, err := template.New(i.Tool.ID).Option("missingkey=zero").Funcs(template.FuncMap{
		"q": quoteForArgv,
	}).Parse(i.Tool.CommandTemplate)
	if err != nil {
		return "", fmt.Errorf("tool %q template: %w", i.Tool.ID, err)
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, i.Args); err != nil {
		return "", fmt.Errorf("tool %q template execution: %w", i.Tool.ID, err)
	}
	return out.String(), nil
}

// quoteForArgv wraps a rendered value in double quotes so the tokeniser keeps it
// as one argument, escaping any character that could terminate the quoting.
func quoteForArgv(value any) string {
	text := ArgValueString(value)
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + replacer.Replace(text) + `"`
}

// Display renders the command for a human, shell-quoted for copy-paste safety.
func (i Invocation) Display() (string, error) {
	tokens, err := i.Argv()
	if err != nil {
		return "", err
	}

	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		quoted = append(quoted, quoteForDisplay(token))
	}
	return strings.Join(quoted, " "), nil
}

// quoteForDisplay wraps a token in single quotes when it contains anything that
// would change meaning in a shell.
func quoteForDisplay(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\n\"'\\$`;&|<>()*?[]{}!#~") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", `'\''`) + "'"
}

// splitArgs tokenises a rendered command, honouring single and double quotes.
func splitArgs(rendered string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
		started bool
	)

	flush := func() {
		if started {
			tokens = append(tokens, current.String())
			current.Reset()
			started = false
		}
	}

	for _, r := range rendered {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			started = true

		case r == '\'' || r == '"':
			quote = r
			started = true

		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()

		default:
			current.WriteRune(r)
			started = true
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("template left an unclosed quote: %w", ErrInvalidArgv)
	}
	flush()

	return tokens, nil
}

// ArgValueString renders a validated argument for logging.
func ArgValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", value)
}
