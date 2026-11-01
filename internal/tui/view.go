package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

// Palette. One dark theme with seven semantic colours; every style below is
// built from these, so re-theming the shell means editing this block only.
const (
	colBackdrop  = "#0d1117" // app background
	colPanel     = "#161b22" // header and footer bars
	colSelection = "#22303f" // cursor wash on the active row
	colInk       = "#e6edf3" // primary text
	colMuted     = "#8b949e" // secondary text, pending stages
	colAccent    = "#7dd3fc" // brand, current stage, focus
	colSuccess   = "#3fb950" // completed stages
	colDanger    = "#f85149" // failed stages
)

// Named styles. Nothing outside this block names a colour.
var (
	styleBackdrop     = lipgloss.NewStyle().Background(lipgloss.Color(colBackdrop))
	styleHeaderBar    = lipgloss.NewStyle().Foreground(lipgloss.Color(colInk)).Background(lipgloss.Color(colPanel)).Bold(true)
	styleFooterBar    = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Background(lipgloss.Color(colPanel))
	styleBrand        = lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Bold(true)
	styleTitle        = lipgloss.NewStyle().Foreground(lipgloss.Color(colInk)).Bold(true)
	styleMuted        = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	styleSelected     = lipgloss.NewStyle().Foreground(lipgloss.Color(colInk)).Background(lipgloss.Color(colSelection)).Bold(true)
	styleSidebar      = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	styleStageCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Bold(true)
	styleStageDone    = lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess))
	styleStageFailed  = lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)
	styleStageSkipped = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Faint(true)
	styleStagePending = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
)

// Layout budgets.
const (
	defaultWidth  = 80
	defaultHeight = 24

	chromeHeight = 3 // header + blank separator + footer
	minSidebar   = 18
	maxSidebar   = 26
)

// emptyStateTitle heads the engagements screen when the list is empty.
const emptyStateTitle = "No engagements yet"

// title is the human-facing name of a screen, used by the header and tests.
func (s Screen) title() string {
	switch s {
	case ScreenEngagements:
		return "Engagements"
	case ScreenNewEngagement:
		return "New Engagement"
	case ScreenDashboard:
		return "Dashboard"
	case ScreenFindings:
		return "Findings"
	case ScreenMemory:
		return "Memory"
	case ScreenSettings:
		return "Settings"
	}
	return "Engagements"
}

// View implements tea.Model.
//
// It reads state only: content and size are settled in Update, so rendering is
// deterministic and repeatable without a program runner.
func (m Model) View() tea.View {
	if m.width <= 0 || m.height <= 0 {
		m = m.resize(defaultWidth, defaultHeight)
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.headerView(),
		"",
		m.bodyView(),
		m.footerView(),
	)

	v := tea.NewView(styleBackdrop.Render(body))
	v.AltScreen = true
	return v
}

// layout helpers ------------------------------------------------------------

func (m Model) sidebarWidth() int {
	if m.width < 40 {
		return 0
	}
	return clampInt(m.width/4, minSidebar, maxSidebar)
}

func (m Model) gapWidth() int {
	if m.sidebarWidth() == 0 {
		return 0
	}
	return 1
}

func (m Model) contentWidth() int {
	return max(1, m.width-m.sidebarWidth()-m.gapWidth())
}

func (m Model) contentHeight() int {
	return max(1, m.height-chromeHeight)
}

// headerView renders operator, screen and stage progress on one bar.
func (m Model) headerView() string {
	left := styleBrand.Render("COSMICS") + styleMuted.Render("  ·  ") + styleTitle.Render(m.screen.title())
	right := styleMuted.Render(truncate(m.rightHeader(), max(0, m.width/2)))
	return styleHeaderBar.Width(m.width).Render(spread(left, right, m.width))
}

// rightHeader builds the plain-text right side of the header.
func (m Model) rightHeader() string {
	parts := []string{m.opts.Operator, progressLabel(m.selectedPointer()), "v" + m.opts.Version}
	return strings.Join(parts, "  ·  ")
}

// progressLabel summarises the pipeline position of an engagement.
func progressLabel(eng *domain.Engagement) string {
	if eng == nil {
		return "no selection"
	}
	if eng.Stage == domain.EngageStageDone {
		return "done"
	}
	kind, ok := eng.Stage.StageKind()
	if !ok {
		return "not started"
	}
	return fmt.Sprintf("%s %d/%d", kind.Title(), kind.Position(), len(domain.AllStages))
}

// bodyView renders the sidebar and the content pane side by side.
func (m Model) bodyView() string {
	pane := m.viewport.View()
	if strings.TrimSpace(pane) == "" {
		pane = limitLines(m.documentBody(), m.contentHeight())
	}

	sw := m.sidebarWidth()
	if sw == 0 {
		return pane
	}

	sidebar := " " + renderStageSidebar(m.selectedPointer(), max(1, sw-1))
	sidebar = limitLines(styleSidebar.Render(sidebar), m.contentHeight())

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, strings.Repeat(" ", m.gapWidth()), pane)
}

// footerView renders the always-visible key hints.
func (m Model) footerView() string {
	hints := " " + truncate(m.keyHints(), max(0, m.width-2))
	return styleFooterBar.Width(m.width).Render(spread(hints, "", m.width))
}

// keyHints is context-sensitive: a focused command line advertises only the
// keys that still work.
func (m Model) keyHints() string {
	if m.input.Focused() {
		return "esc blur   ·   ctrl+c quit"
	}
	if m.overlay != viewNormal {
		return "esc close   ·   ? toggle help   ·   q quit"
	}
	if m.screen != ScreenEngagements {
		return "esc back   ·   ? help   ·   / command   ·   q quit"
	}
	return "↑/k ↓/j move   ·   enter open   ·   n new   ·   f findings   ·   m memory   ·   s settings   ·   ? help   ·   / command   ·   q quit"
}

// selectedPointer returns the engagement under the cursor, or nil.
func (m Model) selectedPointer() *domain.Engagement {
	eng, ok := m.selected()
	if !ok {
		return nil
	}
	return &eng
}

// documentBody renders the active screen (or overlay) as plain content.
func (m Model) documentBody() string {
	switch m.overlay {
	case viewHelp:
		return m.helpBody()
	case viewApproval:
		return m.approvalBody()
	case viewNormal:
	}

	switch m.screen {
	case ScreenEngagements:
		return m.engagementsBody()
	case ScreenDashboard:
		return m.dashboardBody()
	case ScreenNewEngagement:
		return m.placeholderBody("New Engagement", "Draft an engagement: name, client, mode, targets, authorization.")
	case ScreenFindings:
		return m.placeholderBody("Findings", "Confirmed vulnerabilities appear here once the pipeline produces evidence.")
	case ScreenMemory:
		return m.placeholderBody("Memory", "Facts, prior runs and engagement notes live here.")
	case ScreenSettings:
		return m.placeholderBody("Settings", "Provider, model profile and executor configuration live here.")
	}
	return m.engagementsBody()
}

// placeholderBody renders a title plus explanatory copy for a scaffold screen.
func (m Model) placeholderBody(title, copy string) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		styleTitle.Render(title),
		"",
		styleMuted.Width(m.contentWidth()).Render(copy),
	)
}

// engagementsBody renders the list, or the empty state.
func (m Model) engagementsBody() string {
	if len(m.engagements) == 0 {
		return m.emptyBody()
	}
	return renderEngagementTable(m.engagements, m.cursor, m.contentWidth())
}

func (m Model) emptyBody() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		styleTitle.Render(emptyStateTitle),
		"",
		styleMuted.Width(m.contentWidth()).Render(
			"Press n to draft one. Every engagement is kept — draft, running or closed — so nothing is dropped silently."),
	)
}

// dashboardBody renders the selected engagement.
func (m Model) dashboardBody() string {
	eng, ok := m.selected()
	if !ok {
		return m.emptyBody()
	}

	rows := []string{
		styleTitle.Render(truncate(eng.Name, m.contentWidth())),
		"",
		field("Client", blankDash(eng.Client)),
		field("Mode", string(eng.Mode)),
		field("Status", string(eng.Status)),
		field("Stage", progressLabel(&eng)),
		field("Updated", updatedLabel(eng)),
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func field(label, value string) string {
	return styleMuted.Render(fmt.Sprintf("%-10s", label)) + " " + value
}

func blankDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
