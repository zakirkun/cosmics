// Package tui renders the Cosmics terminal shell.
//
// It depends on internal/domain only. Engagement data arrives through messages,
// so every model here is constructible, updatable and renderable without a
// terminal, a database, or a network connection.
package tui

import (
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

// Screen identifies the pane shown in the main content area.
type Screen int

// Screens, in navigation order.
const (
	ScreenEngagements Screen = iota
	ScreenNewEngagement
	ScreenDashboard
	ScreenFindings
	ScreenMemory
	ScreenSettings
)

// viewState identifies a modal overlay drawn above the active screen.
type viewState int

const (
	viewNormal viewState = iota
	viewApproval
	viewHelp
)

// commandPlaceholder is the empty-state text of the command line.
const commandPlaceholder = "type a command…"

// Options carries the immutable shell metadata rendered in the header.
type Options struct {
	Operator string
	DataDir  string
	Version  string

	// Clock supplies the instant recorded when an engagement is drafted. It is
	// injected so a test can draft one without reading the wall clock.
	Clock domain.Clock

	// OnDraft persists a drafted engagement. The shell calls it when a draft is
	// submitted; a nil callback keeps the draft in memory only, which is what a
	// test and a read-only preview want.
	OnDraft func(engagement domain.Engagement, targets []domain.Target) tea.Cmd

	// OnRun persists an engagement and drives it through the pipeline. Typing a
	// target calls this rather than OnDraft, because the operator asked to test a
	// host, not to file a row.
	OnRun func(engagement domain.Engagement, targets []domain.Target) tea.Cmd

	// OnResume runs an engagement that already exists, which is what pressing
	// enter on a draft does.
	OnResume func(engagement domain.Engagement) tea.Cmd

	// LoadFindings, LoadMemory and LoadSettings fetch the data each read-only
	// screen shows. A nil callback leaves that screen empty rather than failing,
	// so a shell can be constructed before the store is wired.
	LoadFindings func(engagementID string) tea.Cmd
	LoadMemory   func(engagementID string) tea.Cmd
	LoadSettings func() tea.Cmd
}

// Model is the root bubbletea model.
//
// It is a value type: Update returns a new Model and never mutates its receiver.
// The viewport is held by pointer because bubbles exposes its setters on a
// pointer receiver; it is still only read during View.
type Model struct {
	opts Options

	width, height int
	screen        Screen
	overlay       viewState
	engagements   []domain.Engagement
	cursor        int
	err           error
	quitting      bool

	clock  domain.Clock
	target TargetInput

	findings FindingsView
	memory   MemoryView
	settings SettingsView

	viewport *viewport.Model
	input    textinput.Model
	spinner  spinner.Model
}

// New constructs an idle shell.
//
// It starts no goroutine and performs no I/O. The application supplies
// engagements later with EngagementsLoadedMsg.
func New(opts Options) Model {
	if opts.Operator == "" {
		opts.Operator = "operator"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}

	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = commandPlaceholder
	input.CharLimit = 512

	vp := viewport.New(viewport.WithWidth(0), viewport.WithHeight(0))

	return Model{
		opts:     opts,
		screen:   ScreenEngagements,
		overlay:  viewNormal,
		viewport: &vp,
		input:    input,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Line)),
		clock:    opts.Clock,
		target:   NewTargetInput(),
		findings: NewFindingsView(),
		memory:   NewMemoryView(),
	}
}

// Init implements tea.Model. The M1 shell has nothing to schedule, so it
// returns no command and therefore starts no goroutine.
func (m Model) Init() tea.Cmd { return nil }

// selected returns a copy of the engagement under the cursor.
func (m Model) selected() (domain.Engagement, bool) {
	if m.cursor < 0 || m.cursor >= len(m.engagements) {
		return domain.Engagement{}, false
	}
	return m.engagements[m.cursor], true
}
