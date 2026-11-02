// Package onboarding implements the first-run configuration wizard: it
// discovers the models a gateway actually serves, lets the operator choose one,
// and writes a configuration file that satisfies config.Validate.
//
// The API key is used to reach the gateway and is never persisted. The saved
// file records only the NAME of the environment variable that holds it.
package onboarding

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
)

// Defaults shared by the wizard, the discoverer, and the CLI.
const (
	DefaultEndpoint     = "http://localhost:20128/v1"
	DefaultProviderName = "9router"
	DefaultAPIKeyEnv    = "NINEROUTER_KEY" //nolint:gosec // G101: an environment-variable NAME, never a credential value
	DefaultProfileName  = "default"
)

// Step identifies the wizard screen currently shown.
type Step int

// Wizard steps in navigation order.
const (
	StepWelcome Step = iota
	StepEndpoint
	StepAPIKey
	StepModel
	StepConfirm
	StepDone
)

// Sentinel errors raised by the pure state machine.
var (
	ErrWizardComplete  = errors.New("onboarding: wizard is already complete")
	ErrMissingModel    = errors.New("onboarding: no model chosen")
	ErrInvalidEndpoint = errors.New("onboarding: endpoint must be an absolute http(s) URL")
)

// State is the whole wizard at one instant. Every transition returns a new
// value, so the state machine is testable without a terminal or a network.
type State struct {
	step      Step
	endpoint  string
	apiKey    string
	available []string
	filtered  []string
	query     string
	selected  int
	model     string
	profile   string
	savedTo   string
}

// NewState starts the wizard at the welcome step with a seeded endpoint.
func NewState(defaultEndpoint string) State {
	endpoint := strings.TrimSpace(defaultEndpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	state := State{
		step:     StepWelcome,
		endpoint: endpoint,
		profile:  DefaultProfileName,
	}
	return state.refilter()
}

// Step reports the active step.
func (s State) Step() Step { return s.step }

// Next advances one step, validating the completed step first.
//
// An empty API key is accepted: a local gateway may run unauthenticated, and
// the key is only needed for discovery, never for the saved file.
func (s State) Next() (State, error) {
	switch s.step {
	case StepWelcome:
		s.step = StepEndpoint

	case StepEndpoint:
		if err := validateEndpoint(s.endpoint); err != nil {
			return s, err
		}
		s.step = StepAPIKey

	case StepAPIKey:
		s.step = StepModel
		s = s.refilter()

	case StepModel:
		if strings.TrimSpace(s.model) == "" {
			return s, ErrMissingModel
		}
		s.step = StepConfirm

	case StepConfirm:
		s.step = StepDone

	case StepDone:
		return s, ErrWizardComplete
	}

	return s, nil
}

// Back retreats one step, stopping at the welcome screen.
func (s State) Back() State {
	if s.step > StepWelcome {
		s.step--
	}
	return s
}

// SetSearch filters the available models by a case-insensitive substring and
// resets the cursor to the first match. An empty query shows everything.
func (s State) SetSearch(query string) State {
	s.query = query
	s.selected = 0
	return s.refilter()
}

// MoveSelection moves the cursor by delta, clamped to the filtered list.
func (s State) MoveSelection(delta int) State {
	if len(s.filtered) == 0 {
		s.selected = 0
		return s
	}

	next := s.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(s.filtered) {
		next = len(s.filtered) - 1
	}
	s.selected = next
	return s
}

// ChosenModel reports the model selected so far, or the empty string.
func (s State) ChosenModel() string { return s.model }

// WithModels installs a freshly discovered catalogue and resets the cursor.
func (s State) WithModels(models []string) State {
	s.available = append([]string(nil), models...)
	s.selected = 0
	return s.refilter()
}

// Choose records the operator's model choice.
func (s State) Choose(model string) State {
	s.model = strings.TrimSpace(model)
	return s
}

// Endpoint reports the gateway URL the wizard will write.
func (s State) Endpoint() string { return s.endpoint }

// WithEndpoint replaces the gateway URL before it is validated by Next.
func (s State) WithEndpoint(endpoint string) State {
	s.endpoint = strings.TrimSpace(endpoint)
	return s
}

// APIKey reports the key held in memory for discovery. It is never persisted.
func (s State) APIKey() string { return s.apiKey }

// WithAPIKey replaces the in-memory key.
func (s State) WithAPIKey(key string) State {
	s.apiKey = key
	return s
}

// Query reports the active search term.
func (s State) Query() string { return s.query }

// Filtered returns the models currently offered.
func (s State) Filtered() []string { return append([]string(nil), s.filtered...) }

// SelectedIndex reports the highlighted position within the filtered list.
func (s State) SelectedIndex() int { return s.selected }

// Choice is one selectable model rendered by the TUI.
type Choice struct {
	Name     string
	Selected bool
}

// Choices renders the filtered list with the highlight applied.
func (s State) Choices() []Choice {
	out := make([]Choice, 0, len(s.filtered))
	for index, name := range s.filtered {
		out = append(out, Choice{Name: name, Selected: index == s.selected})
	}
	return out
}

// WithSavedPath records where the configuration was written.
func (s State) WithSavedPath(path string) State {
	s.savedTo = path
	return s
}

// SavedPath reports where the configuration was written.
func (s State) SavedPath() string { return s.savedTo }

// SelectedModel reports the highlighted model, or the empty string when the
// filtered list is empty.
func (s State) SelectedModel() string {
	if s.selected < 0 || s.selected >= len(s.filtered) {
		return ""
	}
	return s.filtered[s.selected]
}

// BuildConfig turns the wizard result into a validated configuration.
//
// The provider entry records DefaultAPIKeyEnv, never the key itself, so the
// saved YAML cannot leak a credential.
func (s State) BuildConfig() (config.Config, error) {
	model := strings.TrimSpace(s.model)
	if model == "" {
		return config.Config{}, ErrMissingModel
	}

	cfg := config.Default()
	cfg.Providers = map[string]config.Provider{
		DefaultProviderName: {
			Kind:       config.ProviderOpenAICompatible,
			BaseURL:    s.endpoint,
			APIKeyEnv:  DefaultAPIKeyEnv,
			TimeoutS:   120,
			MaxRetries: 2,
		},
	}

	entry := config.ModelEntry{
		Provider:  DefaultProviderName,
		Model:     model,
		MaxTokens: 4096,
	}

	profile := make(config.ModelProfile, len(domain.AllStages)+1)
	for _, stage := range domain.AllStages {
		profile[stage] = []config.ModelEntry{entry}
	}
	profile[config.CompactStageKey] = []config.ModelEntry{entry}

	cfg.ModelProfiles = map[string]config.ModelProfile{s.profileName(): profile}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("validate generated configuration: %w", err)
	}
	return cfg, nil
}

// refilter recomputes the visible list and keeps the cursor in range.
func (s State) refilter() State {
	query := strings.ToLower(strings.TrimSpace(s.query))

	if query == "" {
		s.filtered = append([]string(nil), s.available...)
	} else {
		filtered := make([]string, 0, len(s.available))
		for _, model := range s.available {
			if strings.Contains(strings.ToLower(model), query) {
				filtered = append(filtered, model)
			}
		}
		s.filtered = filtered
	}

	if s.selected >= len(s.filtered) {
		s.selected = len(s.filtered) - 1
	}
	if s.selected < 0 {
		s.selected = 0
	}
	return s
}

// profileName returns the profile key to write, defaulting when unset.
func (s State) profileName() string {
	if strings.TrimSpace(s.profile) == "" {
		return DefaultProfileName
	}
	return s.profile
}

// validateEndpoint rejects anything the provider layer would also reject, so
// the operator sees the problem while they can still fix it.
func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("parse %q: %w", endpoint, ErrInvalidEndpoint)
	}
	if !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%q: %w", endpoint, ErrInvalidEndpoint)
	}
	return nil
}
