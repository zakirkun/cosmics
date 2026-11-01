package memory

import (
	"context"
	"fmt"
)

// factRow is one active fact projection.
type factRow struct {
	subject    string
	predicate  string
	value      string
	confidence float64
	sourceKind string
}

// loadBriefingFacts reads active facts plus the superseded count.
func loadBriefingFacts(ctx context.Context, deps BriefingDeps) ([]factRow, int, error) {
	rows, err := deps.DB.Reader().QueryContext(ctx,
		`SELECT subject, predicate, value, confidence, source_kind
		   FROM facts
		  WHERE engagement_id = ? AND superseded_by IS NULL
		  ORDER BY subject ASC, predicate ASC, created_at DESC, id DESC`,
		deps.Engagement.ID.String())
	if err != nil {
		return nil, 0, fmt.Errorf("briefing facts: %w", err)
	}
	defer rows.Close()

	facts := make([]factRow, 0, 16)
	for rows.Next() {
		var f factRow
		if err := rows.Scan(&f.subject, &f.predicate, &f.value, &f.confidence, &f.sourceKind); err != nil {
			return nil, 0, fmt.Errorf("briefing scan fact: %w", err)
		}
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("briefing iterate facts: %w", err)
	}

	var superseded int
	if err := deps.DB.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM facts WHERE engagement_id = ? AND superseded_by IS NOT NULL`,
		deps.Engagement.ID.String()).Scan(&superseded); err != nil {
		return nil, 0, fmt.Errorf("briefing count superseded facts: %w", err)
	}
	return facts, superseded, nil
}

// factSection renders active facts as predicate[subject] = value.
func factSection(facts []factRow, superseded int) briefingSection {
	lines := make([]string, 0, len(facts))
	for _, f := range facts {
		lines = append(lines, fmt.Sprintf("- %s[%s] = %s (conf %.2f, %s)",
			f.predicate, f.subject, f.value, f.confidence, f.sourceKind))
	}
	if len(lines) == 0 {
		lines = []string{"(none)"}
	}
	title := fmt.Sprintf("Facts (%d active, %d superseded)", len(facts), superseded)
	return briefingSection{title: title, lines: lines}
}
