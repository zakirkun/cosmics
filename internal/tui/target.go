package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrInvalidTarget reports a typed value the domain refused.
var ErrInvalidTarget = errors.New("tui: invalid target")

// Target input offers one field, because that is the whole question a person
// actually has: which host am I testing. The name, the mode and the authorization
// are derived, and every derived value can still be changed later.
const (
	targetPlaceholder = "portal.example.test"
	targetDefaultMode = domain.ModeRecon
)

// TargetInput is the one-line target prompt shown on the new-engagement screen.
type TargetInput struct {
	value string
	err   error
}

// NewTargetInput returns an empty prompt.
func NewTargetInput() TargetInput { return TargetInput{} }

// WithValue sets the typed text.
func (t TargetInput) WithValue(value string) TargetInput {
	t.value = value
	return t
}

// Value reports the typed text.
func (t TargetInput) Value() string { return t.value }

// WithError attaches a failure for display.
func (t TargetInput) WithError(err error) TargetInput {
	t.err = err
	return t
}

// Err reports the attached failure.
func (t TargetInput) Err() error { return t.err }

// Clear resets the prompt.
func (t TargetInput) Clear() TargetInput { return TargetInput{} }

// Parse builds the engagement and its targets from the one typed value.
//
// A comma or space separates several targets, which is how an operator naturally
// writes a short scope. Everything else is delegated to the domain constructors, so
// the same rules apply here as to any other entry point.
func (t TargetInput) Parse(now domain.TimestampMs) (domain.Engagement, []domain.Target, error) {
	raw := strings.TrimSpace(t.value)
	if raw == "" {
		return domain.Engagement{}, nil, fmt.Errorf("type a target to begin: %w", ErrInvalidTarget)
	}

	// Semicolons are rejected rather than treated as separators: a value
	// containing one is far more likely to be an attempted injection than a scope,
	// and splitting it would hand the shell metacharacter to NewTarget piecemeal.
	if strings.ContainsAny(raw, ";&|`$\n") {
		return domain.Engagement{}, nil, fmt.Errorf(
			"target %q contains a shell metacharacter: %w", raw, ErrInvalidTarget)
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' '
	})

	targets := make([]domain.Target, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		target, err := domain.NewTarget(domain.TargetDomain, trimmed)
		if err != nil {
			return domain.Engagement{}, nil, fmt.Errorf("target %q: %w: %w", trimmed, ErrInvalidTarget, err)
		}
		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return domain.Engagement{}, nil, fmt.Errorf("type a target to begin: %w", ErrInvalidTarget)
	}

	engagement, err := domain.NewEngagement(domain.NewEngagementParams{
		Name:          deriveName(targets),
		Client:        "",
		Mode:          targetDefaultMode,
		Authorization: domain.Authorization{AuthorizedBy: "operator", AuthorizedAt: now},
		Targets:       targets,
		Now:           now,
	})
	if err != nil {
		return domain.Engagement{}, nil, fmt.Errorf("draft engagement: %w", err)
	}

	return engagement, targets, nil
}

// deriveName builds a human name from the first target and the extra count.
//
// The name is derived rather than asked for because "Acme external assessment"
// means nothing until the operator knows which host they are pointing at; the
// target is the identifying fact, so the default name says it.
func deriveName(targets []domain.Target) string {
	if len(targets) == 0 {
		return "untitled engagement"
	}

	first := targets[0].Value
	if len(targets) == 1 {
		return first
	}
	return fmt.Sprintf("%s +%d", first, len(targets)-1)
}

// handleTargetKey applies one keystroke to the target prompt.
func (m Model) handleTargetKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.screen = ScreenEngagements
		m.target = NewTargetInput()
		m.err = nil
		return m, nil

	case keyEnter:
		return m.startFromTarget()

	case keyBackspace:
		runes := []rune(m.target.Value())
		if len(runes) == 0 {
			return m, nil
		}
		m.target = m.target.WithValue(string(runes[:len(runes)-1]))
		return m, nil

	case keySpace:
		m.target = m.target.WithValue(m.target.Value() + " ")
		return m, nil
	}

	text := msg.String()
	if text == "" || len(text) > 1 {
		return m, nil
	}
	if strings.ContainsAny(text, "\x00\x1b") {
		return m, nil
	}

	m.target = m.target.WithValue(m.target.Value() + text)
	return m, nil
}

// startFromTarget validates the prompt and starts the run.
//
// The operator typed a host and pressed enter; filing a draft would leave them
// staring at a list with nothing happening. The draft is saved and driven in one
// command, so the first visible result is a stage advancing.
func (m Model) startFromTarget() (Model, tea.Cmd) {
	engagement, targets, err := m.target.Parse(m.now())
	if err != nil {
		m.target = m.target.WithError(err)
		m.err = err
		return m, nil
	}

	m.target = NewTargetInput()
	m.err = nil
	m.screen = ScreenDashboard

	return m, m.runCmd(engagement, targets)
}

// handleTargetPaste appends pasted text to the prompt.
func (m Model) handleTargetPaste(content string) (Model, tea.Cmd) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return m, nil
	}
	m.target = m.target.WithValue(strings.TrimSpace(m.target.Value() + " " + clean))
	return m, nil
}

func (m Model) now() domain.TimestampMs {
	if m.clock == nil {
		return 0
	}
	return m.clock.Now()
}

// targetBody renders the one-line prompt.
func (m Model) targetBody() string {
	rows := []string{
		styleTitle.Render("New engagement"),
		"",
		styleMuted.Render("Type a target. A comma or space separates several."),
		"",
		styleBrand.Render("› ") + m.target.Value() + styleBrand.Render("▌"),
	}

	if err := m.target.Err(); err != nil {
		rows = append(rows, "", styleError.Render(err.Error()))
	}

	rows = append(rows, "",
		styleMuted.Render("enter start · esc cancel · ctrl+v paste"))

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
