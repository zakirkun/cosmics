package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// severityOrder fixes the reporting order and the count breakdown.
var severityOrder = []string{"critical", "high", "medium", "low", "info"}

// findingRow is one finding projection.
type findingRow struct {
	severity   string
	confidence string
	title      string
	endpoint   sql.NullString
	asset      sql.NullString
}

// loadFindings reads findings ordered by severity then title.
func loadFindings(ctx context.Context, deps BriefingDeps) ([]findingRow, error) {
	const query = `SELECT f.severity, f.confidence, f.title, f.endpoint, a.value
	                 FROM findings f
	                 LEFT JOIN assets a ON a.id = f.asset_id
	                WHERE f.engagement_id = ?
	                ORDER BY CASE f.severity
	                           WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2
	                           WHEN 'low' THEN 3 ELSE 4 END,
	                         f.title ASC, f.id ASC`

	rows, err := deps.DB.Reader().QueryContext(ctx, query, deps.Engagement.ID.String())
	if err != nil {
		return nil, fmt.Errorf("briefing findings: %w", err)
	}
	defer rows.Close()

	findings := make([]findingRow, 0, 16)
	for rows.Next() {
		var f findingRow
		if err := rows.Scan(&f.severity, &f.confidence, &f.title, &f.endpoint, &f.asset); err != nil {
			return nil, fmt.Errorf("briefing scan finding: %w", err)
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("briefing iterate findings: %w", err)
	}
	return findings, nil
}

// findingSection renders findings with their severity breakdown.
func findingSection(findings []findingRow) briefingSection {
	counts := make(map[string]int, len(severityOrder))
	for _, f := range findings {
		counts[f.severity]++
	}
	breakdown := make([]string, 0, len(severityOrder))
	for _, severity := range severityOrder {
		if counts[severity] > 0 {
			breakdown = append(breakdown, fmt.Sprintf("%d %s", counts[severity], severity))
		}
	}
	title := fmt.Sprintf("Findings (%d)", len(findings))
	if len(breakdown) > 0 {
		title = fmt.Sprintf("Findings (%d: %s)", len(findings), strings.Join(breakdown, ", "))
	}

	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		line := fmt.Sprintf("- [%s, %s] %s", f.severity, f.confidence, f.title)
		if f.endpoint.Valid && f.endpoint.String != "" {
			line += " — " + f.endpoint.String
		}
		if f.asset.Valid && f.asset.String != "" {
			line += " (asset " + f.asset.String + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"(none)"}
	}
	return briefingSection{title: title, lines: lines}
}

// eventRow is one episodic memory record.
type eventRow struct {
	createdAt domain.TimestampMs
	level     string
	kind      string
	message   string
}

// loadEvents reads the newest briefingEventLimit events, oldest first so the
// rendered window reads chronologically.
func loadEvents(ctx context.Context, deps BriefingDeps) ([]eventRow, error) {
	rows, err := deps.DB.Reader().QueryContext(ctx,
		`SELECT created_at, level, kind, message FROM events
		  WHERE engagement_id = ?
		  ORDER BY created_at DESC, id DESC LIMIT ?`,
		deps.Engagement.ID.String(), briefingEventLimit)
	if err != nil {
		return nil, fmt.Errorf("briefing events: %w", err)
	}
	defer rows.Close()

	events := make([]eventRow, 0, briefingEventLimit)
	for rows.Next() {
		var (
			e         eventRow
			createdAt int64
		)
		if err := rows.Scan(&createdAt, &e.level, &e.kind, &e.message); err != nil {
			return nil, fmt.Errorf("briefing scan event: %w", err)
		}
		e.createdAt = domain.TimestampMs(createdAt)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("briefing iterate events: %w", err)
	}

	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

// eventSection renders the event window.
func eventSection(events []eventRow, now domain.TimestampMs) briefingSection {
	lines := make([]string, 0, len(events))
	for _, e := range events {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s: %s",
			relativeTime(now, e.createdAt), e.level, e.kind, clip(e.message, briefingMessageChars)))
	}
	if len(lines) == 0 {
		lines = []string{"(none)"}
	}
	return briefingSection{title: fmt.Sprintf("Recent activity (%d events)", len(events)), lines: lines}
}

// budgetSection renders token and cost consumption from the aggregate.
func budgetSection(deps BriefingDeps) briefingSection {
	e := deps.Engagement
	return briefingSection{
		title: "Budget",
		lines: []string{fmt.Sprintf("tokens %s/%s, cost %s/%s",
			formatCount(e.TokensUsed), formatCount(e.TokenBudget),
			formatUSD(e.SpentUSDMicros), formatUSD(e.BudgetUSDMicros))},
	}
}
