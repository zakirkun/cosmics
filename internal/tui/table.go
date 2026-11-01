package tui

import (
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// Engagement table column budgets. The name column absorbs all slack: every
// other column is bounded so a long name can never push a status off-screen.
const (
	colModeWidth    = 8
	colStatusWidth  = 10
	colStageWidth   = 10
	colUpdatedWidth = 16
	tableGutter     = 2
	tableMinNameCol = 8
	minTableWidth   = 40
)

// engagementColumns is the resolved width of each column for a given pane.
type engagementColumns struct {
	name    int
	mode    int
	status  int
	stage   int
	updated int
}

// resolveColumns distributes the pane width across the five columns.
func resolveColumns(width int) engagementColumns {
	fixed := colModeWidth + colStatusWidth + colStageWidth + colUpdatedWidth + tableGutter*4
	return engagementColumns{
		name:    max(tableMinNameCol, width-fixed),
		mode:    colModeWidth,
		status:  colStatusWidth,
		stage:   colStageWidth,
		updated: colUpdatedWidth,
	}
}

// renderEngagementTable renders the list with a header row and a cursor wash.
//
// It degrades to a compact two-column list below minTableWidth rather than
// clipping columns, because a table wedged into 30 columns is unreadable.
func renderEngagementTable(engagements []domain.Engagement, cursor, width int) string {
	if width < minTableWidth {
		return renderCompactList(engagements, cursor, width)
	}

	cols := resolveColumns(width)
	header := styleMuted.Render(
		padCell("NAME", cols.name) +
			padCell("MODE", cols.mode) +
			padCell("STATUS", cols.status) +
			padCell("STAGE", cols.stage) +
			padCell("UPDATED", cols.updated),
	)

	rows := make([]string, 0, len(engagements)+2)
	rows = append(rows, header, styleMuted.Render(strings.Repeat("─", max(1, width))))

	for i, eng := range engagements {
		cells := []string{
			padCell(eng.Name, cols.name),
			padCell(string(eng.Mode), cols.mode),
			padCell(string(eng.Status), cols.status),
			padCell(stageLabel(eng), cols.stage),
			padCell(updatedLabel(eng), cols.updated),
		}
		row := strings.Join(cells, strings.Repeat(" ", tableGutter))

		if i == cursor {
			rows = append(rows, styleSelected.Render(padCell(row, width)))
			continue
		}
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

// renderCompactList is the narrow-terminal fallback: name and status only.
func renderCompactList(engagements []domain.Engagement, cursor, width int) string {
	rows := make([]string, 0, len(engagements))
	for i, eng := range engagements {
		row := padCell(eng.Name, max(1, width-colStatusWidth-1)) + " " +
			padCell(string(eng.Status), min(colStatusWidth, max(1, width-1)))
		row = truncate(row, width)
		if i == cursor {
			row = styleSelected.Render(padCell(row, width))
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

const emDash = "\u2014"

// stageLabel is the short pipeline pointer shown in the table.
func stageLabel(eng domain.Engagement) string {
	switch eng.Stage {
	case domain.EngageStageNone:
		return emDash
	case domain.EngageStageDone:
		return "done"
	case domain.EngageStageRecon, domain.EngageStageOSINT, domain.EngageStageAnalysis,
		domain.EngageStageMapper, domain.EngageStageExploit, domain.EngageStageReporting:
		kind, ok := eng.Stage.StageKind()
		if !ok {
			return emDash
		}
		return string(kind)
	}
	return emDash
}

// updatedLabel renders UpdatedAt as a UTC wall clock, or a dash when unset.
func updatedLabel(eng domain.Engagement) string {
	if eng.UpdatedAt.IsZero() {
		return emDash
	}
	return eng.UpdatedAt.Time().Format("2006-01-02 15:04")
}
