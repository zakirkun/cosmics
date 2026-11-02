package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// Update implements tea.Model.
//
// It returns a command only for a real side effect — quitting, focusing the
// command line (cursor blink), or advancing the spinner. Pure navigation
// returns nil, which makes key handling testable without a runtime.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg.Width, msg.Height), nil

	case tea.KeyPressMsg:
		next, cmd := m.handleKey(msg)
		return next.sync(), cmd

	case tea.PasteMsg:
		next, cmd := m.handlePaste(msg.Content)
		return next.sync(), cmd

	case tea.QuitMsg:
		m.quitting = true
		return m, nil

	case EngagementsLoadedMsg:
		return m.applyEngagements(msg).sync(), nil

	case EngagementDraftedMsg:
		return m.applyDrafted(msg).sync(), nil

	case RunStartedMsg:
		return m.handleRunStarted(msg).sync(), nil

	case RunProgressMsg:
		return m.handleRunProgress(msg).sync(), nil

	case RunFinishedMsg:
		return m.handleRunFinished(msg).sync(), nil

	case FindingsLoadedMsg:
		m.findings = m.findings.WithRows(msg.Findings)
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m.sync(), nil

	case MemoryFactsLoadedMsg:
		m.memory = m.memory.WithFacts(msg.Facts)
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m.sync(), nil

	case SettingsLoadedMsg:
		m.settings = msg.Settings
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m.sync(), nil

	case ErrorMsg:
		m.err = msg.Err
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

// resize stores the terminal size and reflows every sub-component.
func (m Model) resize(width, height int) Model {
	m.width = width
	m.height = height
	m.input.SetWidth(max(0, width-2))
	return m.sync()
}

// sync reflows the content viewport to the current size and document.
func (m Model) sync() Model {
	m.viewport.SetWidth(m.contentWidth())
	m.viewport.SetHeight(m.contentHeight())
	m.viewport.SetContent(m.documentBody())
	return m
}

// handleKey routes one key press.
//
// The command line owns printable input while it is focused: navigation and
// screen shortcuts are skipped entirely, so typing "q" inserts a character
// instead of quitting. ctrl+c remains unconditional — an operator must always
// be able to leave the program.
func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	if m.input.Focused() {
		if msg.String() == "esc" {
			m.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// The new-engagement prompt owns the keyboard while it is open, so a target
	// containing "n", "f", "m" or "s" is typed rather than treated as a shortcut.
	if m.screen == ScreenNewEngagement && m.overlay == viewNormal {
		return m.handleTargetKey(msg)
	}

	if m.screen == ScreenFindings && m.overlay == viewNormal {
		if consumed, next := m.handleFindingsKey(msg); consumed {
			return next, nil
		}
	}

	if m.screen == ScreenMemory && m.overlay == viewNormal {
		if consumed, next := m.handleMemoryKey(msg); consumed {
			return next, nil
		}
	}

	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.engagements)-1 {
			m.cursor++
		}

	case "enter":
		engagement, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.screen = ScreenDashboard

		if m.screenIsRunnable(engagement) && m.opts.OnResume != nil {
			return m, m.opts.OnResume(engagement)
		}
		return m, nil

	case "esc":
		switch {
		case m.overlay != viewNormal:
			m.overlay = viewNormal
		case m.screen != ScreenEngagements:
			m.screen = ScreenEngagements
		}

	case "n":
		m.screen = ScreenNewEngagement
		m.target = NewTargetInput()
		return m, nil

	case "f":
		m.screen = ScreenFindings
		return m, m.loadFindings()

	case "m":
		m.screen = ScreenMemory
		return m, m.loadMemory()

	case "s":
		m.screen = ScreenSettings
		return m, m.loadSettings()

	case "?":
		if m.overlay == viewHelp {
			m.overlay = viewNormal
		} else {
			m.overlay = viewHelp
		}

	case "/":
		return m, m.input.Focus()
	}

	return m, nil
}

// applyEngagements replaces the list, or records the failure and keeps the
// previous list so a transient load error does not blank the screen.
func (m Model) applyEngagements(msg EngagementsLoadedMsg) Model {
	if msg.Err != nil {
		m.err = msg.Err
		return m
	}

	m.err = nil
	m.engagements = msg.Engagements

	if m.cursor >= len(m.engagements) {
		m.cursor = len(m.engagements) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// applyDrafted appends a newly validated engagement and selects it.
//
// The engagement is not persisted here: the application owns the write, so a
// failed insert arrives later as ErrorMsg and does not silently vanish.
func (m Model) applyDrafted(msg EngagementDraftedMsg) Model {
	m.err = nil
	m.engagements = append(m.engagements, msg.Engagement)
	m.cursor = len(m.engagements) - 1
	m.screen = ScreenEngagements
	return m
}

func (m Model) selectedID() string {
	engagement, ok := m.selected()
	if !ok {
		return ""
	}
	return engagement.ID.String()
}

func (m Model) loadFindings() tea.Cmd {
	if m.opts.LoadFindings == nil {
		return nil
	}
	return m.opts.LoadFindings(m.selectedID())
}

func (m Model) loadMemory() tea.Cmd {
	if m.opts.LoadMemory == nil {
		return nil
	}
	return m.opts.LoadMemory(m.selectedID())
}

func (m Model) loadSettings() tea.Cmd {
	if m.opts.LoadSettings == nil {
		return nil
	}
	return m.opts.LoadSettings()
}
