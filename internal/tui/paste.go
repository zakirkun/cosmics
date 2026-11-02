package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handlePaste routes pasted text to whichever field owns the keyboard.
//
// bubbletea delivers a bracketed paste as a PasteMsg rather than as keys, so a
// model that only handles KeyPressMsg silently drops every paste. bubbles'
// textinput already knows how to insert a PasteMsg, so the work here is routing it
// to the right input rather than re-implementing insertion.
func (m Model) handlePaste(content string) (Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}

	if m.input.Focused() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.PasteMsg{Content: content})
		return m, cmd
	}

	if m.screen == ScreenNewEngagement && m.overlay == viewNormal {
		return m.handleTargetPaste(content)
	}

	return m, nil
}
