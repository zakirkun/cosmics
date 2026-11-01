package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// helpBody documents every binding.
func (m Model) helpBody() string {
	lines := []string{
		styleTitle.Render("Keys"),
		"",
		binding("↑ / k", "move the cursor up"),
		binding("↓ / j", "move the cursor down"),
		binding("enter", "open the selected engagement"),
		binding("esc", "close an overlay, or return to the list"),
		binding("n", "new engagement"),
		binding("f", "findings"),
		binding("m", "memory"),
		binding("s", "settings"),
		binding("?", "toggle this help"),
		binding("/", "focus the command line"),
		binding("q", "quit (only when the command line is blurred)"),
		binding("ctrl+c", "quit from anywhere"),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// binding renders one key/description row.
func binding(key, description string) string {
	return styleBrand.Render(fmt.Sprintf("%-8s", key)) + " " + styleMuted.Render(description)
}

// approvalBody is the M1 placeholder for the approval modal; the engine wires the
// real content in a later milestone.
func (m Model) approvalBody() string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colDanger)).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			styleTitle.Render("APPROVAL REQUIRED"),
			"",
			styleMuted.Render("No action is pending in this milestone."),
		))
}
