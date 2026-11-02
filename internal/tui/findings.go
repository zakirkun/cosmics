package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// Findings table column budgets. The title column absorbs all slack so a long
// title can never push severity or status off the pane.
const (
	colSeverityWidth      = 8
	colCategoryWidth      = 12
	colConfidenceWidth    = 10
	colFindingStatusWidth = 10
	minFindingsWidth      = 40
)

// colWarning is the only palette entry findings needs beyond the shared block:
// the medium band sits between accent and danger.
const colWarning = "#d29922"

// severityStyles colours one severity band. Nothing else names these colours.
var severityStyles = map[string]lipgloss.Style{
	"critical": lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true),
	"high":     lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)),
	"medium":   lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)),
	"low":      lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)),
	"info":     lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
}

// findingSeverityRank orders findings from most to least urgent. A lexical sort
// would put "high" before "medium" and rank "critical" only by spelling.
var findingSeverityRank = map[string]int{
	"critical": 0,
	"high":     1,
	"medium":   2,
	"low":      3,
	"info":     4,
}

// FindingRow is one findings-table row.
//
// The shell deliberately does not import internal/store: the application maps a
// stored finding into this type at the message boundary, so rendering stays pure.
type FindingRow struct {
	ID         string
	Title      string
	Category   string
	Severity   string
	Confidence string
	Status     string
	Verified   bool
}

// FindingsView is the findings screen state: rows, a cursor, and a severity
// filter. It is a value type; every mutation returns a new view.
type FindingsView struct {
	rows     []FindingRow
	cursor   int
	severity string
}

// NewFindingsView returns an empty view with no filter.
func NewFindingsView() FindingsView { return FindingsView{} }

// WithRows replaces the rows, ordered by severity rank then title then id.
func (v FindingsView) WithRows(rows []FindingRow) FindingsView {
	sorted := make([]FindingRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if findingSeverityRank[left.Severity] != findingSeverityRank[right.Severity] {
			return findingSeverityRank[left.Severity] < findingSeverityRank[right.Severity]
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		return left.ID < right.ID
	})

	v.rows = sorted
	return v.clampCursor()
}

// MoveCursor moves the cursor by delta, clamped to the visible rows.
func (v FindingsView) MoveCursor(delta int) FindingsView {
	v.cursor = clampInt(v.cursor+delta, 0, max(0, len(v.Visible())-1))
	return v
}

// SetFilter restricts the table to one severity. An empty value shows every
// severity; a name that matches no row shows an empty, explanatory table.
func (v FindingsView) SetFilter(severity string) FindingsView {
	v.severity = strings.ToLower(strings.TrimSpace(severity))
	return v.clampCursor()
}

// Cursor returns the index of the highlighted visible row.
func (v FindingsView) Cursor() int { return v.cursor }

// Visible returns the filtered rows in rank order.
func (v FindingsView) Visible() []FindingRow {
	if v.severity == "" {
		return v.rows
	}
	out := make([]FindingRow, 0, len(v.rows))
	for _, row := range v.rows {
		if row.Severity == v.severity {
			out = append(out, row)
		}
	}
	return out
}

// Selected returns the row under the cursor.
func (v FindingsView) Selected() (FindingRow, bool) {
	visible := v.Visible()
	if v.cursor < 0 || v.cursor >= len(visible) {
		return FindingRow{}, false
	}
	return visible[v.cursor], true
}

// clampCursor keeps the cursor inside the visible set after rows or a filter
// change.
func (v FindingsView) clampCursor() FindingsView {
	if visible := len(v.Visible()); visible > 0 {
		v.cursor = clampInt(v.cursor, 0, visible-1)
	} else {
		v.cursor = 0
	}
	return v
}

// RenderFindings renders the table, the filtered-empty state, or the
// never-loaded state with the next action named.
func RenderFindings(v FindingsView, width int) string {
	width = max(1, width)
	rows := v.Visible()

	if len(rows) == 0 {
		return renderFindingsEmpty(v, width)
	}

	lines := make([]string, 0, len(rows)+3)
	if v.severity != "" {
		lines = append(lines, styleMuted.Render(
			fmt.Sprintf("filter: %s · %d shown · press 0 to clear", v.severity, len(rows))), "")
	}

	if width < minFindingsWidth {
		for i, row := range rows {
			lines = append(lines, renderFindingCompact(row, i == v.cursor, width))
		}
		return strings.Join(lines, "\n")
	}

	cols := resolveFindingsColumns(width)
	lines = append(lines,
		styleMuted.Render(
			padCell("SEVERITY", cols.severity)+
				padCell("TITLE", cols.title)+
				padCell("CATEGORY", cols.category)+
				padCell("CONFIDENCE", cols.confidence)+
				padCell("STATUS", cols.status)),
		styleMuted.Render(strings.Repeat("─", max(1, width))),
	)

	for i, row := range rows {
		severity := padCell(row.Severity, cols.severity)
		cells := []string{
			severity,
			padCell(row.Title, cols.title),
			padCell(row.Category, cols.category),
			padCell(row.Confidence, cols.confidence),
			padCell(row.Status, cols.status),
		}

		if i == v.cursor {
			lines = append(lines, styleSelected.Render(
				padCell(strings.Join(cells, strings.Repeat(" ", tableGutter)), width)))
			continue
		}
		cells[0] = severityStyle(row.Severity).Render(severity)
		lines = append(lines, strings.Join(cells, strings.Repeat(" ", tableGutter)))
	}

	return strings.Join(lines, "\n")
}

// findingsColumns is the resolved width of each column for one pane.
type findingsColumns struct {
	severity   int
	title      int
	category   int
	confidence int
	status     int
}

// resolveFindingsColumns distributes the pane width across the five columns.
func resolveFindingsColumns(width int) findingsColumns {
	fixed := colSeverityWidth + colCategoryWidth + colConfidenceWidth +
		colFindingStatusWidth + tableGutter*4
	return findingsColumns{
		severity:   colSeverityWidth,
		title:      max(tableMinNameCol, width-fixed),
		category:   colCategoryWidth,
		confidence: colConfidenceWidth,
		status:     colFindingStatusWidth,
	}
}

// renderFindingCompact is the narrow-terminal fallback: severity and title only.
func renderFindingCompact(row FindingRow, selected bool, width int) string {
	severity := padCell(blankDash(row.Severity), min(colSeverityWidth, width))
	title := truncate(row.Title, max(1, width-colSeverityWidth-1))
	line := severity + " " + title
	if selected {
		return styleSelected.Render(padCell(line, width))
	}
	return line
}

// renderFindingsEmpty explains how rows arrive and, when a filter hides them,
// how to widen the view.
func renderFindingsEmpty(v FindingsView, width int) string {
	bodyWidth := max(1, width)

	if v.severity != "" {
		return lipgloss.JoinVertical(lipgloss.Left,
			styleTitle.Render(fmt.Sprintf("No findings at severity %q", v.severity)),
			"",
			styleMuted.Width(bodyWidth).Render(
				"Press 0 to clear the filter. The other severity bands still hold every "+
					"recorded finding, so the table is filtered rather than empty."),
		)
	}

	if len(v.rows) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			styleTitle.Render("No findings recorded"),
			"",
			styleMuted.Width(bodyWidth).Render(
				"Run an engagement first — cosmics run --engagement <id> — then reload this "+
					"screen with r. Findings are written by the analysis and exploit stages; an "+
					"empty table means no row was recorded, not that the target is clean."),
		)
	}

	// Rows exist but the filter matched none of them; the message above cannot
	// tell those two states apart, so name the unfiltered count here.
	return lipgloss.JoinVertical(lipgloss.Left,
		styleTitle.Render("No findings in this view"),
		"",
		styleMuted.Width(bodyWidth).Render(
			fmt.Sprintf("%d recorded finding(s) exist, but none match the current view. "+
				"Press 0 to clear the filter.", len(v.rows))),
	)
}

// severityStyle returns the colour for a severity, defaulting to muted so an
// unknown value never renders invisibly.
func severityStyle(severity string) lipgloss.Style {
	if style, ok := severityStyles[severity]; ok {
		return style
	}
	return styleMuted
}
