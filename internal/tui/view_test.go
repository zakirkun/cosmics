package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_View_produces_non_empty_content_for_every_screen(t *testing.T) {
	tests := []struct {
		name   string
		screen Screen
		title  string
	}{
		{"engagements", ScreenEngagements, "Engagements"},
		{"new engagement", ScreenNewEngagement, "New Engagement"},
		{"dashboard", ScreenDashboard, "Dashboard"},
		{"findings", ScreenFindings, "Findings"},
		{"memory", ScreenMemory, "Memory"},
		{"settings", ScreenSettings, "Settings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			m := loadedModel(t)
			m.screen = tt.screen

			// When
			view := m.View()

			// Then
			require.NotEmpty(t, view.Content)
			assert.Contains(t, stripANSI(view.Content), tt.title)
		})
	}
}

func Test_View_enables_the_alternate_screen_when_rendered(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	view := m.View()

	// Then
	assert.True(t, view.AltScreen)
}

func Test_View_shows_the_empty_state_when_no_engagements_exist(t *testing.T) {
	// Given
	m := New(Options{Operator: "tester"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)

	// When
	content := stripANSI(m.View().Content)

	// Then
	assert.Contains(t, content, "No engagements")
}

func Test_View_lists_every_engagement_name_when_engagements_are_loaded(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	content := stripANSI(m.View().Content)

	// Then
	for _, eng := range m.engagements {
		assert.Contains(t, content, eng.Name)
	}
}

func Test_View_includes_the_operator_and_version_when_rendered(t *testing.T) {
	// Given
	m := New(Options{Operator: "alice", Version: "9.9.9"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)

	// When
	content := stripANSI(m.View().Content)

	// Then
	assert.Contains(t, content, "alice")
	assert.Contains(t, content, "9.9.9")
}

func Test_View_renders_the_help_overlay_when_it_is_open(t *testing.T) {
	// Given
	m := loadedModel(t)
	opened, _ := m.Update(key('?'))
	m = opened.(Model)

	// When
	content := stripANSI(m.View().Content)

	// Then
	assert.Contains(t, content, "Keys")
	assert.Contains(t, content, "toggle this help")
}

func Test_View_fits_within_the_terminal_width_when_sized(t *testing.T) {
	// Given
	m := loadedModel(t)
	const width = 100
	sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = sized.(Model)

	// When
	content := stripANSI(m.View().Content)

	// Then
	for _, line := range strings.Split(content, "\n") {
		assert.LessOrEqual(t, len([]rune(line)), width, "line exceeded width: %q", line)
	}
}

func Test_View_renders_a_stage_sidebar_entry_for_every_stage(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	content := stripANSI(m.View().Content)

	// Then
	for _, stage := range []string{"Recon", "OSINT", "Analysis", "Mapper", "Exploit", "Reporting"} {
		assert.Contains(t, content, stage)
	}
}
