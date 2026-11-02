package tui

import tea "charm.land/bubbletea/v2"

// Key names used by more than one screen handler.
const (
	keyEsc       = "esc"
	keyUp        = "up"
	keyDown      = "down"
	keyCursorUp  = "k"
	keyCursorDn  = "j"
	keyBackspace = "backspace"
	keySpace     = "space"
	keyEnter     = "enter"
	keyTab       = "tab"
	keyShiftTab  = "shift+tab"
	keyCtrlS     = "ctrl+s"
	keyCtrlT     = "ctrl+t"
	keyLeft      = "left"
	keyRight     = "right"
	keyCtrlC     = "ctrl+c"
)

// handleFindingsKey applies the findings screen key map.
//
// It reports whether the key was consumed so the caller can fall through to the
// global shortcuts. Returning a bare Model would make "s" open settings from a
// severity filter, which is not what an operator expects.
func (m Model) handleFindingsKey(msg tea.KeyPressMsg) (bool, Model) {
	switch msg.String() {
	case keyUp, keyCursorUp:
		m.findings = m.findings.MoveCursor(-1)
		return true, m
	case keyDown, keyCursorDn:
		m.findings = m.findings.MoveCursor(1)
		return true, m
	case "1":
		m.findings = m.findings.SetFilter("critical")
		return true, m
	case "2":
		m.findings = m.findings.SetFilter("high")
		return true, m
	case "3":
		m.findings = m.findings.SetFilter("medium")
		return true, m
	case "4":
		m.findings = m.findings.SetFilter("low")
		return true, m
	case "0":
		m.findings = m.findings.SetFilter("")
		return true, m
	}
	return false, m
}

// handleMemoryKey applies the memory screen key map.
func (m Model) handleMemoryKey(msg tea.KeyPressMsg) (bool, Model) {
	switch msg.String() {
	case keyUp, keyCursorUp:
		m.memory = m.memory.MoveCursor(-1)
		return true, m
	case keyDown, keyCursorDn:
		m.memory = m.memory.MoveCursor(1)
		return true, m
	}
	return false, m
}
