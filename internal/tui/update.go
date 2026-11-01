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

	case tea.QuitMsg:
		m.quitting = true
		return m, nil

	case EngagementsLoadedMsg:
		return m.applyEngagements(msg).sync(), nil

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
		if _, ok := m.selected(); ok {
			m.screen = ScreenDashboard
		}

	case "esc":
		switch {
		case m.overlay != viewNormal:
			m.overlay = viewNormal
		case m.screen != ScreenEngagements:
			m.screen = ScreenEngagements
		}

	case "n":
		m.screen = ScreenNewEngagement

	case "f":
		m.screen = ScreenFindings

	case "m":
		m.screen = ScreenMemory

	case "s":
		m.screen = ScreenSettings

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
