package onboarding

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cosmics/cosmics/internal/config"
)

// palette holds the wizard's colours in one place so every screen agrees.
const (
	colAccent   = "#7C5CFF"
	colAccent2  = "#22D3EE"
	colInk      = "#E7E9F0"
	colMuted    = "#8B90A6"
	colPanel    = "#161826"
	colBackdrop = "#0B0C12"
	colSuccess  = "#38D39F"
	colWarning  = "#F5B759"
	colError    = "#FF6B6B"
	colSelect   = "#262A3D"
)

var (
	styleFrame = lipgloss.NewStyle().
			Background(lipgloss.Color(colBackdrop)).
			Foreground(lipgloss.Color(colInk)).
			Padding(1, 3)

	styleTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colAccent)).
			Bold(true)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colMuted))

	styleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colAccent2)).
			Bold(true)

	styleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colInk))

	styleHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colMuted)).
			Italic(true)

	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.Color(colSelect)).
			Foreground(lipgloss.Color(colInk)).
			Bold(true)

	styleMarker = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colAccent2)).
			Bold(true)

	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess))
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning))
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color(colError))

	styleRule = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
)

// ModelsMsg carries a discovery result into the model.
type ModelsMsg struct {
	Models []string
	Err    error
}

// SavedMsg carries the result of the final write.
type SavedMsg struct {
	Path string
	Err  error
}

// AbortMsg asks the program to quit without saving.
type AbortMsg struct{}

// TUIOptions configures the wizard program.
type TUIOptions struct {
	// DefaultEndpoint seeds the endpoint prompt.
	DefaultEndpoint string

	// Discover fetches the model list. It may be nil, in which case the model
	// screen accepts a typed identifier only.
	Discover Discoverer

	// Save writes the built config. It may be nil, in which case the wizard
	// returns the config to its caller instead of writing it.
	Save func(context.Context, config.Config) (string, error)
}

// TUI is the bubbletea model for the wizard.
type TUI struct {
	state State
	opts  TUIOptions

	width  int
	height int

	typing bool
	status string
	load   bool
	saved  bool

	quitting bool
	config   config.Config
	savedTo  string
}

// NewTUI starts the wizard at the welcome screen.
func NewTUI(opts TUIOptions) TUI {
	return TUI{
		state:  NewState(opts.DefaultEndpoint),
		opts:   opts,
		width:  72,
		height: 24,
	}
}

// Init implements tea.Model.
func (m TUI) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m TUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		return m, nil

	case ModelsMsg:
		m.load = false
		if typed.Err != nil {
			m.status = styleErr.Render("discovery failed: " + typed.Err.Error())
			return m, nil
		}
		m.state = m.state.WithModels(typed.Models)
		if len(typed.Models) == 0 {
			m.status = styleWarn.Render("the gateway returned no models; type an identifier")
			return m, nil
		}
		m.status = styleOK.Render(fmt.Sprintf("%d model(s) discovered", len(typed.Models)))
		return m, nil

	case SavedMsg:
		m.load = false
		if typed.Err != nil {
			m.status = styleErr.Render("could not save: " + typed.Err.Error())
			return m, nil
		}
		m.saved = true
		m.savedTo = typed.Path
		return m, tea.Quit

	case AbortMsg:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyPressMsg:
		return m.handleKey(typed)

	case tea.PasteMsg:
		return m.handlePaste(typed.Content)
	}

	return m, nil
}

// handleKey routes one keystroke for the current screen.
func (m TUI) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	if m.state.Step() == StepDone {
		return m, tea.Quit
	}

	if key.String() == "esc" {
		if m.state.Step() == StepWelcome {
			return m, func() tea.Msg { return AbortMsg{} }
		}
		m.state = m.state.Back()
		m.typing = false
		return m, nil
	}

	if key.String() == "enter" {
		return m.advance()
	}

	return m.handleTyping(key)
}

// advance moves forward, dispatching discovery or saving where required.
func (m TUI) advance() (tea.Model, tea.Cmd) {
	if m.state.Step() == StepModel && m.opts.Discover != nil && len(m.state.Filtered()) == 0 && !m.load {
		m.load = true
		m.status = styleHint.Render("contacting the gateway…")

		endpoint := m.state.Endpoint()
		apiKey := m.state.APIKey()
		discover := m.opts.Discover

		return m, func() tea.Msg {
			models, err := discover.Models(context.Background(), endpoint, apiKey)
			return ModelsMsg{Models: models, Err: err}
		}
	}

	next, err := m.state.Next()
	if err != nil {
		m.status = styleErr.Render(err.Error())
		return m, nil
	}
	m.state = next
	m.status = ""
	m.typing = false

	if m.state.Step() == StepDone && m.opts.Save != nil && !m.saved {
		built, buildErr := m.state.BuildConfig()
		if buildErr != nil {
			m.status = styleErr.Render(buildErr.Error())
			m.state = m.state.Back()
			return m, nil
		}
		m.config = built
		m.load = true

		save := m.opts.Save
		return m, func() tea.Msg {
			path, saveErr := save(context.Background(), built)
			return SavedMsg{Path: path, Err: saveErr}
		}
	}

	return m, nil
}

// handleTyping applies a keystroke to whichever field the current screen edits.
func (m TUI) handleTyping(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	text := key.String()

	switch m.state.Step() {
	case StepWelcome, StepConfirm, StepDone:
		return m, nil
	case StepEndpoint:
		m.state = m.state.WithEndpoint(editText(m.state.Endpoint(), text))
	case StepAPIKey:
		m.state = m.state.WithAPIKey(editText(m.state.APIKey(), text))
	case StepModel:
		if len(m.state.Filtered()) > 0 {
			switch text {
			case "up", "k":
				m.state = m.state.MoveSelection(-1)
				return m, nil
			case "down", "j":
				m.state = m.state.MoveSelection(1)
				return m, nil
			}
		}
		m.state = m.state.SetSearch(editText(m.state.Query(), text))
		m.state = m.state.Choose(m.state.SelectedModel())
	}

	return m, nil
}

// handlePaste inserts pasted text into the field the current step edits.
//
// bubbletea delivers a bracketed paste as its own message, not as keys, so a paste
// needs its own path: without one, pasting an API key inserted nothing at all.
func (m TUI) handlePaste(content string) (tea.Model, tea.Cmd) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return m, nil
	}

	switch m.state.Step() {
	case StepEndpoint:
		m.state = m.state.WithEndpoint(m.state.Endpoint() + clean)
	case StepAPIKey:
		m.state = m.state.WithAPIKey(m.state.APIKey() + clean)
	case StepModel:
		m.state = m.state.SetSearch(m.state.Query() + clean)
		m.state = m.state.Choose(m.state.SelectedModel())
	case StepWelcome, StepConfirm, StepDone:
		return m, nil
	}

	return m, nil
}

// editText applies one keystroke to a line of text.
//
// A bracketed paste arrives as a single key whose Text is the whole clip: that is
// why the printable branch appends the entire string rather than one rune.
func editText(current, key string) string {
	switch key {
	case "backspace":
		runes := []rune(current)
		if len(runes) == 0 {
			return current
		}
		return string(runes[:len(runes)-1])
	case "space":
		return current + " "
	case "enter", "esc", "tab", "up", "down", "left", "right",
		"home", "end", "delete", "pgup", "pgdown", "insert":
		return current
	}

	if key == "" {
		return current
	}

	for _, char := range key {
		if char < 0x20 || char == 0x7f {
			return current
		}
	}
	return current + key
}

// View implements tea.Model.
func (m TUI) View() tea.View {
	var body strings.Builder

	body.WriteString(styleTitle.Render("COSMICS"))
	body.WriteString(styleSubtitle.Render("  ·  first-run setup"))
	body.WriteString("\n")
	body.WriteString(styleRule.Render(strings.Repeat("─", clampWidth(m.width-6, 20, 68))))
	body.WriteString("\n\n")

	body.WriteString(m.stepBar())
	body.WriteString("\n\n")

	switch m.state.Step() {
	case StepWelcome:
		body.WriteString(m.viewWelcome())
	case StepEndpoint:
		body.WriteString(m.viewEndpoint())
	case StepAPIKey:
		body.WriteString(m.viewAPIKey())
	case StepModel:
		body.WriteString(m.viewModel())
	case StepConfirm:
		body.WriteString(m.viewConfirm())
	case StepDone:
		body.WriteString(m.viewDone())
	}

	if m.status != "" {
		body.WriteString("\n")
		body.WriteString(m.status)
		body.WriteString("\n")
	}

	body.WriteString("\n")
	body.WriteString(styleHint.Render(m.keyHints()))

	view := tea.NewView(styleFrame.Width(clampWidth(m.width, 40, 100)).Render(body.String()))
	view.AltScreen = true
	return view
}

// stepBar renders the progress indicator.
func (m TUI) stepBar() string {
	labels := []string{"endpoint", "key", "model", "confirm"}
	parts := make([]string, 0, len(labels))
	current := int(m.state.Step())

	for index, label := range labels {
		marker := fmt.Sprintf("%d %s", index+1, label)
		switch {
		case index+1 < current:
			parts = append(parts, styleOK.Render("✓ "+label))
		case index+1 == current:
			parts = append(parts, styleMarker.Render("● "+marker))
		default:
			parts = append(parts, styleSubtitle.Render("○ "+label))
		}
	}
	return strings.Join(parts, styleRule.Render("  ──  "))
}

// viewWelcome renders the opening screen.
func (m TUI) viewWelcome() string {
	var builder strings.Builder

	builder.WriteString(styleLabel.Render("Welcome"))
	builder.WriteString("\n\n")
	builder.WriteString(styleValue.Render(
		"This wizard writes ~/.cosmics/config.yaml.\n" +
			"It asks for your gateway, discovers the models it serves,\n" +
			"and lets you pick one."))
	builder.WriteString("\n\n")
	builder.WriteString(styleHint.Render(
		"The API key is used only to reach the gateway and is never written.\n" +
			"The file records the name of the environment variable instead."))

	return builder.String()
}

// viewEndpoint renders the endpoint prompt.
func (m TUI) viewEndpoint() string {
	var builder strings.Builder

	builder.WriteString(styleLabel.Render("Gateway endpoint"))
	builder.WriteString("\n\n")
	builder.WriteString(styleValue.Render("Base URL of the OpenAI-compatible gateway:"))
	builder.WriteString("\n\n")
	builder.WriteString("  ")
	builder.WriteString(styleMarker.Render("› "))
	builder.WriteString(m.state.Endpoint())
	builder.WriteString(styleMarker.Render("█"))

	return builder.String()
}

// viewAPIKey renders the key prompt, showing only a masked tail.
func (m TUI) viewAPIKey() string {
	var builder strings.Builder

	builder.WriteString(styleLabel.Render("API key"))
	builder.WriteString("\n\n")
	builder.WriteString(styleValue.Render("Optional for a local gateway. It is never written to disk."))
	builder.WriteString("\n\n")
	builder.WriteString("  ")
	builder.WriteString(styleMarker.Render("› "))
	builder.WriteString(maskKey(m.state.APIKey()))
	builder.WriteString(styleMarker.Render("█"))

	return builder.String()
}

// maskKey shows only the last four characters.
func maskKey(key string) string {
	if key == "" {
		return styleHint.Render("(empty)")
	}
	runes := []rune(key)
	if len(runes) <= 4 {
		return strings.Repeat("•", len(runes))
	}
	return strings.Repeat("•", len(runes)-4) + string(runes[len(runes)-4:])
}

// viewModel renders the searchable model list.
func (m TUI) viewModel() string {
	var builder strings.Builder

	builder.WriteString(styleLabel.Render("Model"))
	builder.WriteString("\n")

	if m.load {
		builder.WriteString(styleHint.Render("  contacting the gateway…"))
		builder.WriteString("\n")
		return builder.String()
	}

	builder.WriteString(styleValue.Render("  Search: "))
	builder.WriteString(m.state.Query())
	builder.WriteString(styleMarker.Render("█"))
	builder.WriteString("\n\n")

	choices := m.state.Choices()
	if len(choices) == 0 {
		builder.WriteString(styleHint.Render(
			"  No model matches. Keep typing, or press esc then retype the filter."))
		builder.WriteString("\n")
		return builder.String()
	}

	const visible = 8
	start, end := window(len(choices), m.state.SelectedIndex(), visible)

	for index := start; index < end; index++ {
		choice := choices[index]
		line := "  " + choice.Name
		if choice.Selected {
			builder.WriteString(styleSelected.Render("› " + line[2:]))
			builder.WriteString(styleMarker.Render("  ← enter"))
		} else {
			builder.WriteString(styleValue.Render(line))
		}
		builder.WriteString("\n")
	}

	if len(choices) > visible {
		builder.WriteString(styleHint.Render(fmt.Sprintf(
			"  %d of %d shown", end-start, len(choices))))
		builder.WriteString("\n")
	}

	return builder.String()
}

// window centres a visible slice on the selection.
func window(total, selected, size int) (int, int) {
	if total <= size {
		return 0, total
	}

	start := selected - size/2
	if start < 0 {
		start = 0
	}
	if start+size > total {
		start = total - size
	}
	return start, start + size
}

// viewConfirm renders the summary before writing.
func (m TUI) viewConfirm() string {
	var builder strings.Builder

	builder.WriteString(styleLabel.Render("Confirm"))
	builder.WriteString("\n\n")
	builder.WriteString(renderRow("Endpoint", m.state.Endpoint()))
	builder.WriteString(renderRow("API key", maskKey(m.state.APIKey())))
	builder.WriteString(renderRow("Model", m.state.ChosenModel()))
	builder.WriteString(renderRow("Key env var", DefaultAPIKeyEnv))
	builder.WriteString("\n")
	builder.WriteString(styleHint.Render("The file will be written with 0600 permissions."))

	return builder.String()
}

// renderRow renders one label and value pair.
func renderRow(label, value string) string {
	return fmt.Sprintf("  %s %s\n",
		styleLabel.Render(padRight(label+":", 13)),
		styleValue.Render(value))
}

// padRight pads a string with spaces to width.
func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
}

// viewDone renders the closing screen.
func (m TUI) viewDone() string {
	var builder strings.Builder

	builder.WriteString(styleOK.Render("✓ Configuration written"))
	builder.WriteString("\n\n")
	builder.WriteString(renderRow("Model", m.state.ChosenModel()))
	builder.WriteString("\n")
	builder.WriteString(styleHint.Render(
		"Set " + DefaultAPIKeyEnv + " in your environment, then run `cosmics doctor`."))

	return builder.String()
}

// keyHints renders the footer for the current screen.
func (m TUI) keyHints() string {
	if m.state.Step() == StepDone {
		return "enter quit"
	}

	switch m.state.Step() {
	case StepWelcome:
		return "enter start   ·   esc quit   ·   ctrl+c quit"
	case StepModel:
		return "type to search   ·   ↑/↓ move   ·   enter select   ·   esc back"
	case StepEndpoint, StepAPIKey, StepConfirm, StepDone:
		return "enter continue   ·   esc back   ·   ctrl+c quit"
	}
	return "enter continue   ·   esc back   ·   ctrl+c quit"
}

// clampWidth bounds a width so the frame stays readable.
func clampWidth(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// Run starts the wizard and returns the written path.
func Run(ctx context.Context, opts TUIOptions) (string, error) {
	program := tea.NewProgram(NewTUI(opts), tea.WithContext(ctx))

	final, err := program.Run()
	if err != nil {
		return "", fmt.Errorf("run wizard: %w", err)
	}

	model, ok := final.(TUI)
	if !ok {
		return "", fmt.Errorf("wizard returned an unexpected model: %w", ErrAborted)
	}
	if model.quitting && !model.saved {
		return "", ErrAborted
	}
	if !model.saved {
		return "", fmt.Errorf("wizard finished without saving: %w", ErrAborted)
	}
	if model.savedTo == "" {
		return "", fmt.Errorf("wizard reported a save without a path: %w", ErrAborted)
	}
	return model.savedTo, nil
}
