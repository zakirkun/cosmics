package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// labelColumn is the width of the settings label column.
const labelColumn = 16

// SettingsView is the effective configuration shown by the settings screen.
//
// APIKeyEnv names the environment variable; APIKeySet records whether that
// variable currently holds a value. The value itself is never carried here — the
// screen's whole job is to prove the key is configured without echoing it.
type SettingsView struct {
	Endpoint    string
	Profile     string
	Model       string
	DataDir     string
	ModeDefault string
	ToolImage   string
	APIKeyEnv   string
	APIKeySet   bool
}

// RenderSettings renders the effective configuration as a labelled list.
func RenderSettings(v SettingsView, width int) string {
	width = max(1, width)

	rows := []string{
		styleTitle.Render("Effective configuration"),
		"",
		settingsField("Endpoint", v.Endpoint, width),
		settingsField("Model profile", v.Profile, width),
		settingsField("Model", v.Model, width),
		settingsField("Data directory", v.DataDir, width),
		settingsField("Default mode", v.ModeDefault, width),
		settingsField("Tool image", v.ToolImage, width),
		settingsField("API key", settingsKeyState(v), width),
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// settingsField renders one label/value pair with the value clipped to the
// remaining width, so a long path can never push the pane out of bounds.
func settingsField(label, value string, width int) string {
	valueWidth := max(1, width-labelColumn-1)
	return styleMuted.Render(padCell(label, labelColumn)) + " " +
		truncate(blankDash(value), valueWidth)
}

// settingsKeyState renders presence, never the secret.
func settingsKeyState(v SettingsView) string {
	name := strings.TrimSpace(v.APIKeyEnv)
	if name == "" {
		name = "API key"
	}
	if v.APIKeySet {
		return name + " · set"
	}
	return name + " · not set"
}
