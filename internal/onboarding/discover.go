package onboarding

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/llm/openai"
)

// ErrAborted is returned when input ends before the wizard finished, so a piped
// or closed stdin terminates instead of looping forever.
var ErrAborted = errors.New("onboarding: aborted")

// Discovery bounds.
const (
	discoverTimeout = 15 * time.Second
	maxModelTries   = 3
)

// digitPattern matches a menu selection: digits only, so "1a" stays a search
// term instead of being rejected as a bad number.
var digitPattern = regexp.MustCompile(`^[0-9]+$`)

// Discoverer resolves the model identifiers one gateway serves.
type Discoverer interface {
	Models(ctx context.Context, endpoint, apiKey string) ([]string, error)
}

// GatewayDiscoverer asks an OpenAI-compatible gateway for its catalogue.
type GatewayDiscoverer struct{}

// Models implements Discoverer against endpoint.
func (GatewayDiscoverer) Models(ctx context.Context, endpoint, apiKey string) ([]string, error) {
	client, err := openai.New(openai.Options{
		BaseURL: endpoint,
		APIKey:  apiKey,
		Timeout: discoverTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("build gateway client: %w", err)
	}
	return client.ListModels(ctx)
}

// Interactive runs the line-oriented wizard and returns a validated config.
//
// Every prompt reads exactly one line, so the flow is driven by any io.Reader:
// a terminal, a pipe, or a bytes.Buffer in a test. A nil discoverer skips the
// gateway and asks for a model id directly.
func Interactive(ctx context.Context, in io.Reader, out io.Writer, discoverer Discoverer) (config.Config, error) {
	s := &session{reader: bufio.NewReader(in), out: out, discoverer: discoverer}
	return s.run(ctx)
}

// session holds the I/O handles and the pure state of one wizard run.
type session struct {
	reader     *bufio.Reader
	out        io.Writer
	discoverer Discoverer
	state      State
}

// run walks the steps in order and builds the configuration at the end.
func (s *session) run(ctx context.Context) (config.Config, error) {
	s.state = NewState(DefaultEndpoint)

	for _, step := range []func(context.Context) error{
		s.welcome,
		s.promptEndpoint,
		s.promptAPIKey,
		s.chooseModel,
		s.confirm,
	} {
		if err := step(ctx); err != nil {
			return config.Config{}, err
		}
	}

	cfg, err := s.state.BuildConfig()
	if err != nil {
		return config.Config{}, fmt.Errorf("build configuration: %w", err)
	}
	return cfg, nil
}

// welcome explains what the wizard will do and enters the endpoint step.
func (s *session) welcome(context.Context) error {
	s.print("Cosmics first-run setup\n")
	s.print("The API key is used only to reach the gateway and is never written.\n\n")

	next, err := s.state.Next()
	if err != nil {
		return fmt.Errorf("start wizard: %w", err)
	}
	s.state = next
	return nil
}

// promptEndpoint offers the default and accepts an override.
func (s *session) promptEndpoint(context.Context) error {
	s.print("Gateway endpoint [%s]: ", s.state.Endpoint())

	line, err := s.readLine()
	if err != nil {
		return err
	}

	next := s.state
	if line != "" {
		next = next.WithEndpoint(line)
	}

	advanced, err := next.Next()
	if err != nil {
		return fmt.Errorf("endpoint: %w", err)
	}
	s.state = advanced
	return nil
}

// promptAPIKey reads the key into memory only; it is never echoed or saved.
func (s *session) promptAPIKey(context.Context) error {
	s.print("API key (optional for a local gateway, never saved): ")

	line, err := s.readLine()
	if err != nil {
		return err
	}
	s.state = s.state.WithAPIKey(line)

	next, err := s.state.Next()
	if err != nil {
		return fmt.Errorf("api key: %w", err)
	}
	s.state = next
	return nil
}

// chooseModel discovers the catalogue, then lets the operator pick from it.
func (s *session) chooseModel(ctx context.Context) error {
	models, err := s.discover(ctx)
	if err != nil {
		return err
	}

	s.state = s.state.WithModels(models)
	return s.pickModel()
}

// discover retries the gateway and falls back to a manually typed id.
func (s *session) discover(ctx context.Context) ([]string, error) {
	if s.discoverer == nil {
		return s.manualModel()
	}

	for attempt := 1; attempt <= maxModelTries; attempt++ {
		s.print("Discovering models at %s …\n", s.state.Endpoint())

		models, err := s.discoverer.Models(ctx, s.state.Endpoint(), s.state.APIKey())
		switch {
		case err == nil && len(models) > 0:
			s.print("Found %d model(s).\n", len(models))
			return models, nil
		case err == nil:
			s.print("The gateway returned no models.\n")
		default:
			s.print("Discovery failed: %v\n", err)
		}

		if attempt == maxModelTries {
			break
		}

		s.print("Press enter to retry, or type a model id to continue: ")
		line, readErr := s.readLine()
		if readErr != nil {
			return nil, readErr
		}
		if line != "" {
			return []string{line}, nil
		}
	}
	return s.manualModel()
}

// manualModel asks for a model id when discovery cannot be used.
func (s *session) manualModel() ([]string, error) {
	for attempt := 1; attempt <= maxModelTries; attempt++ {
		s.print("Model id: ")

		line, err := s.readLine()
		if err != nil {
			return nil, err
		}
		if line != "" {
			return []string{line}, nil
		}
		s.print("A model id is required.\n")
	}
	return nil, fmt.Errorf("%w: no model id supplied", ErrAborted)
}

// pickModel renders the numbered list and reads one line per iteration.
//
// A number selects that entry, any other text filters, and an empty line
// accepts the highlighted entry and advances.
func (s *session) pickModel() error {
	for {
		s.renderModels()
		s.print("\nNumber, search term, or enter to accept: ")

		line, err := s.readLine()
		if err != nil {
			return err
		}

		switch {
		case line == "" && len(s.state.Filtered()) > 0:
			return s.acceptModel(s.state.SelectedModel())

		case line == "":
			s.print("Nothing matches %q. Clear the search and try again.\n", s.state.Query())

		case digitPattern.MatchString(line):
			index, convErr := strconv.Atoi(line)
			if convErr != nil {
				return fmt.Errorf("%w: %q", ErrAborted, line)
			}
			if index < 1 || index > len(s.state.Filtered()) {
				s.print("Choose a number between 1 and %d.\n", len(s.state.Filtered()))
				continue
			}
			return s.acceptModel(s.state.Filtered()[index-1])

		default:
			s.state = s.state.SetSearch(line)
		}
	}
}

// renderModels prints the filtered catalogue with the cursor marked.
func (s *session) renderModels() {
	filtered := s.state.Filtered()
	if len(filtered) == 0 {
		s.print("\n  (no models match)\n")
		return
	}

	s.print("\n")
	for index, model := range filtered {
		marker := "  "
		if index == s.state.SelectedIndex() {
			marker = "> "
		}
		s.print("%s%d. %s\n", marker, index+1, model)
	}
}

// acceptModel records the choice and moves to confirmation.
func (s *session) acceptModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("%w: %w", ErrMissingModel, ErrAborted)
	}

	s.print("Selected: %s\n", model)
	next, err := s.state.Choose(model).Next()
	if err != nil {
		return fmt.Errorf("select model: %w", err)
	}
	s.state = next
	return nil
}

// confirm restates the plan and requires explicit approval.
func (s *session) confirm(context.Context) error {
	s.print("\nEndpoint:    %s\n", s.state.Endpoint())
	s.print("Model:       %s\n", s.state.ChosenModel())
	s.print("Key env var: %s (the key itself is not written)\n", DefaultAPIKeyEnv)
	s.print("Write this configuration? [Y/n]: ")

	line, err := s.readLine()
	if err != nil {
		return err
	}
	if line != "" && !isYes(line) {
		return fmt.Errorf("%w: configuration not confirmed", ErrAborted)
	}

	next, err := s.state.Next()
	if err != nil {
		return fmt.Errorf("confirm: %w", err)
	}
	s.state = next
	return nil
}

// readLine reads one line, reporting EOF as ErrAborted.
func (s *session) readLine() (string, error) {
	line, err := s.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", ErrAborted
		}
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// print writes a prompt. A broken writer is not fatal: the caller needs the
// resulting configuration, not the transcript.
func (s *session) print(format string, args ...any) {
	_, _ = fmt.Fprintf(s.out, format, args...)
}

// isYes accepts the affirmative spellings an operator is likely to type.
func isYes(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
