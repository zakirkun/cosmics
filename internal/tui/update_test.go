package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

// key builds the KeyPressMsg a terminal would deliver for a printable rune.
func key(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// specialKey builds the KeyPressMsg for a named key such as enter or esc.
func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// twoEngagements returns a deterministic pair of engagements for list tests.
func twoEngagements(t *testing.T) []domain.Engagement {
	t.Helper()

	authorization := domain.Authorization{
		AuthorizedBy: "tester",
		AuthorizedAt: domain.TimestampMs(1735689600000),
	}

	first, err := domain.NewEngagement(domain.NewEngagementParams{
		Name:          "alpha",
		Mode:          domain.ModeRecon,
		Authorization: authorization,
		Targets:       []domain.Target{{Kind: domain.TargetDomain, Value: "alpha.test"}},
		Now:           domain.TimestampMs(1735689600000),
	})
	require.NoError(t, err)

	second, err := domain.NewEngagement(domain.NewEngagementParams{
		Name:          "beta",
		Mode:          domain.ModeFull,
		Authorization: authorization,
		Targets:       []domain.Target{{Kind: domain.TargetDomain, Value: "beta.test"}},
		Now:           domain.TimestampMs(1735689601000),
	})
	require.NoError(t, err)

	return []domain.Engagement{first, second}
}

// loadedModel returns a sized model holding two engagements.
func loadedModel(t *testing.T) Model {
	t.Helper()

	m := New(Options{Operator: "tester"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	updated, _ = m.Update(EngagementsLoadedMsg{Engagements: twoEngagements(t)})
	return updated.(Model)
}

func Test_Model_moves_the_cursor_down_when_down_is_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	updated, _ := m.Update(specialKey(tea.KeyDown))
	got := updated.(Model)

	// Then
	assert.Equal(t, 1, got.cursor)
}

func Test_Model_moves_the_cursor_up_when_up_is_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)
	down, _ := m.Update(specialKey(tea.KeyDown))
	m = down.(Model)

	// When
	updated, _ := m.Update(specialKey(tea.KeyUp))
	got := updated.(Model)

	// Then
	assert.Equal(t, 0, got.cursor)
}

func Test_Model_moves_the_cursor_when_j_and_k_are_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	down, _ := m.Update(specialKey('j'))
	afterDown := down.(Model)
	up, _ := afterDown.Update(specialKey('k'))
	afterUp := up.(Model)

	// Then
	assert.Equal(t, 1, afterDown.cursor)
	assert.Equal(t, 0, afterUp.cursor)
}

func Test_Model_clamps_the_cursor_when_navigating_past_the_last_engagement(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	for range 10 {
		updated, _ := m.Update(specialKey(tea.KeyDown))
		m = updated.(Model)
	}

	// Then
	assert.Equal(t, len(m.engagements)-1, m.cursor)
}

func Test_Model_clamps_the_cursor_when_navigating_above_the_first_engagement(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	for range 10 {
		updated, _ := m.Update(specialKey(tea.KeyUp))
		m = updated.(Model)
	}

	// Then
	assert.Equal(t, 0, m.cursor)
}

func Test_Model_quits_when_q_is_pressed_and_input_is_not_focused(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	updated, cmd := m.Update(key('q'))
	got := updated.(Model)

	// Then
	assert.True(t, got.quitting)
	require.NotNil(t, cmd)
}

func Test_Model_does_not_quit_when_q_is_typed_into_the_focused_command_line(t *testing.T) {
	// Given
	m := loadedModel(t)
	focused, _ := m.Update(specialKey('/'))
	m = focused.(Model)
	require.True(t, m.input.Focused(), "the command line must be focused for this test to mean anything")

	// When
	updated, _ := m.Update(key('q'))
	got := updated.(Model)

	// Then
	assert.False(t, got.quitting, "q must type into the command line, not quit")
	assert.Equal(t, ScreenEngagements, got.screen)
}

func Test_Model_blurs_the_command_line_when_esc_is_pressed_while_focused(t *testing.T) {
	// Given
	m := loadedModel(t)
	focused, _ := m.Update(specialKey('/'))
	m = focused.(Model)
	require.True(t, m.input.Focused())

	// When
	updated, _ := m.Update(specialKey(tea.KeyEsc))
	got := updated.(Model)

	// Then
	assert.False(t, got.input.Focused())
	assert.Equal(t, ScreenEngagements, got.screen, "esc must blur before it navigates")
}

func Test_Model_opens_the_dashboard_when_enter_is_pressed_on_a_selection(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	updated, _ := m.Update(specialKey(tea.KeyEnter))
	got := updated.(Model)

	// Then
	assert.Equal(t, ScreenDashboard, got.screen)
}

func Test_Model_ignores_enter_when_the_list_is_empty(t *testing.T) {
	// Given
	m := New(Options{Operator: "tester"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)

	// When
	updated, _ := m.Update(specialKey(tea.KeyEnter))
	got := updated.(Model)

	// Then
	assert.Equal(t, ScreenEngagements, got.screen)
}

func Test_Model_returns_to_the_list_when_esc_is_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)
	opened, _ := m.Update(specialKey(tea.KeyEnter))
	m = opened.(Model)
	require.Equal(t, ScreenDashboard, m.screen)

	// When
	updated, _ := m.Update(specialKey(tea.KeyEsc))
	got := updated.(Model)

	// Then
	assert.Equal(t, ScreenEngagements, got.screen)
}

func Test_Model_closes_an_overlay_before_changing_screen_when_esc_is_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)
	help, _ := m.Update(key('?'))
	m = help.(Model)
	require.Equal(t, viewHelp, m.overlay)

	// When
	updated, _ := m.Update(specialKey(tea.KeyEsc))
	got := updated.(Model)

	// Then
	assert.Equal(t, viewNormal, got.overlay)
	assert.Equal(t, ScreenEngagements, got.screen)
}

func Test_Model_navigates_to_each_screen_when_its_shortcut_is_pressed(t *testing.T) {
	tests := []struct {
		name string
		key  rune
		want Screen
	}{
		{"new engagement", 'n', ScreenNewEngagement},
		{"findings", 'f', ScreenFindings},
		{"memory", 'm', ScreenMemory},
		{"settings", 's', ScreenSettings},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			m := loadedModel(t)

			// When
			updated, _ := m.Update(key(tt.key))
			got := updated.(Model)

			// Then
			assert.Equal(t, tt.want, got.screen)
		})
	}
}

func Test_Model_toggles_the_help_overlay_when_question_mark_is_pressed_twice(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	opened, _ := m.Update(key('?'))
	afterOpen := opened.(Model)
	closed, _ := afterOpen.Update(key('?'))
	afterClose := closed.(Model)

	// Then
	assert.Equal(t, viewHelp, afterOpen.overlay)
	assert.Equal(t, viewNormal, afterClose.overlay)
}

func Test_Model_stores_the_error_when_EngagementsLoadedMsg_carries_one(t *testing.T) {
	// Given
	m := loadedModel(t)
	before := m.engagements
	sentinel := assert.AnError

	// When
	updated, _ := m.Update(EngagementsLoadedMsg{Err: sentinel})
	got := updated.(Model)

	// Then
	require.ErrorIs(t, got.err, sentinel)
	assert.Equal(t, before, got.engagements, "a failed load must not blank the existing list")
}

func Test_Model_replaces_the_list_when_EngagementsLoadedMsg_arrives(t *testing.T) {
	// Given
	m := New(Options{Operator: "tester"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	engagements := twoEngagements(t)

	// When
	updated, _ := m.Update(EngagementsLoadedMsg{Engagements: engagements})
	got := updated.(Model)

	// Then
	assert.Equal(t, engagements, got.engagements)
	assert.NoError(t, got.err)
}

func Test_Model_clamps_the_cursor_when_a_smaller_list_arrives(t *testing.T) {
	// Given
	m := loadedModel(t)
	down, _ := m.Update(specialKey(tea.KeyDown))
	m = down.(Model)
	require.Equal(t, 1, m.cursor)

	// When
	updated, _ := m.Update(EngagementsLoadedMsg{Engagements: m.engagements[:1]})
	got := updated.(Model)

	// Then
	assert.Equal(t, 0, got.cursor)
}

func Test_Model_stores_the_terminal_size_when_a_resize_arrives(t *testing.T) {
	// Given
	m := New(Options{Operator: "tester"})

	// When
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	got := updated.(Model)

	// Then
	assert.Equal(t, 100, got.width)
	assert.Equal(t, 30, got.height)
}

func Test_Model_marks_itself_quitting_when_a_quit_message_arrives(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	updated, _ := m.Update(tea.QuitMsg{})
	got := updated.(Model)

	// Then
	assert.True(t, got.quitting)
}

func Test_Model_focuses_the_command_line_when_slash_is_pressed(t *testing.T) {
	// Given
	m := loadedModel(t)

	// When
	updated, cmd := m.Update(specialKey('/'))
	got := updated.(Model)

	// Then
	assert.True(t, got.input.Focused())
	require.NotNil(t, cmd, "focusing a text input returns its cursor-blink command")
}
