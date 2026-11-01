package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// engagementSection renders name, mode, stage, and elapsed time.
func engagementSection(deps BriefingDeps) briefingSection {
	stage := string(deps.Engagement.Stage)
	if stage == "" {
		stage = "none"
	}
	elapsed := "not started"
	if !deps.Engagement.StartedAt.IsZero() {
		elapsed = formatElapsed(deps.Now - deps.Engagement.StartedAt)
	}
	return briefingSection{
		title: "Engagement",
		lines: []string{
			"name: " + deps.Engagement.Name,
			"mode: " + string(deps.Engagement.Mode),
			"stage: " + stage,
			"elapsed: " + elapsed,
		},
	}
}

// targetRow is one seeded target.
type targetRow struct {
	kind  string
	value string
}

// loadTargets reads the operator-supplied seed targets in a stable order.
func loadTargets(ctx context.Context, deps BriefingDeps) ([]targetRow, error) {
	rows, err := deps.DB.Reader().QueryContext(ctx,
		`SELECT kind, value FROM engagement_targets WHERE engagement_id = ?
		  ORDER BY kind ASC, value ASC`, deps.Engagement.ID.String())
	if err != nil {
		return nil, fmt.Errorf("briefing targets: %w", err)
	}
	defer rows.Close()

	targets := make([]targetRow, 0, 8)
	for rows.Next() {
		var t targetRow
		if err := rows.Scan(&t.kind, &t.value); err != nil {
			return nil, fmt.Errorf("briefing scan target: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("briefing iterate targets: %w", err)
	}
	return targets, nil
}

// targetSection lists the seed targets.
func targetSection(targets []targetRow) briefingSection {
	lines := make([]string, 0, len(targets))
	for _, t := range targets {
		lines = append(lines, fmt.Sprintf("- %s (%s)", t.value, t.kind))
	}
	if len(lines) == 0 {
		lines = []string{"(none)"}
	}
	return briefingSection{title: fmt.Sprintf("Targets (%d)", len(targets)), lines: lines}
}

// assetRow is one discovered asset.
type assetRow struct {
	id       string
	kind     string
	value    string
	state    string
	lastSeen domain.TimestampMs
}

// serviceRow is one service observed on an asset.
type serviceRow struct {
	assetID string
	port    int
	proto   string
	service string
	product string
	version string
}

// loadAssets reads assets and their services. Both projections are read in full
// before any formatting so no query runs while another result set is open.
func loadAssets(ctx context.Context, deps BriefingDeps) ([]assetRow, []serviceRow, error) {
	assetQuery := `SELECT id, kind, value, state, last_seen_at FROM assets
	                WHERE engagement_id = ?
	                ORDER BY kind ASC, value ASC, id ASC`
	rows, err := deps.DB.Reader().QueryContext(ctx, assetQuery, deps.Engagement.ID.String())
	if err != nil {
		return nil, nil, fmt.Errorf("briefing assets: %w", err)
	}
	defer rows.Close()

	assets := make([]assetRow, 0, 32)
	for rows.Next() {
		var (
			a        assetRow
			lastSeen int64
		)
		if err := rows.Scan(&a.id, &a.kind, &a.value, &a.state, &lastSeen); err != nil {
			return nil, nil, fmt.Errorf("briefing scan asset: %w", err)
		}
		a.lastSeen = domain.TimestampMs(lastSeen)
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("briefing iterate assets: %w", err)
	}

	serviceQuery := `SELECT s.asset_id, s.port, s.protocol,
	                        COALESCE(s.service, ''), COALESCE(s.product, ''), COALESCE(s.version, '')
	                   FROM services s
	                   JOIN assets a ON a.id = s.asset_id
	                  WHERE a.engagement_id = ?
	                  ORDER BY s.asset_id ASC, s.port ASC, s.protocol ASC`
	serviceRows, err := deps.DB.Reader().QueryContext(ctx, serviceQuery, deps.Engagement.ID.String())
	if err != nil {
		return nil, nil, fmt.Errorf("briefing services: %w", err)
	}
	defer serviceRows.Close()

	services := make([]serviceRow, 0, 32)
	for serviceRows.Next() {
		var s serviceRow
		if err := serviceRows.Scan(&s.assetID, &s.port, &s.proto, &s.service, &s.product, &s.version); err != nil {
			return nil, nil, fmt.Errorf("briefing scan service: %w", err)
		}
		services = append(services, s)
	}
	if err := serviceRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("briefing iterate services: %w", err)
	}
	return assets, services, nil
}

// assetKindOrder fixes the order asset kinds are counted in.
var assetKindOrder = []string{
	"domain", "subdomain", "ip", "host", "url", "endpoint", "repo", "bucket", "mobile_app", "api",
}

// assetSection renders the asset count by kind and one block per asset.
func assetSection(assets []assetRow, services []serviceRow, now domain.TimestampMs) briefingSection {
	counts := make(map[string]int, len(assetKindOrder))
	for _, a := range assets {
		counts[a.kind]++
	}
	breakdown := make([]string, 0, len(assetKindOrder))
	for _, kind := range assetKindOrder {
		if counts[kind] > 0 {
			breakdown = append(breakdown, fmt.Sprintf("%d %s", counts[kind], kind))
		}
	}
	title := fmt.Sprintf("Assets (%d)", len(assets))
	if len(breakdown) > 0 {
		title = fmt.Sprintf("Assets (%d: %s)", len(assets), strings.Join(breakdown, ", "))
	}

	byAsset := make(map[string][]serviceRow, len(assets))
	for _, s := range services {
		byAsset[s.assetID] = append(byAsset[s.assetID], s)
	}

	lines := make([]string, 0, len(assets)*2)
	for _, a := range assets {
		lines = append(lines, fmt.Sprintf("- %s %s [%s, seen %s]",
			a.kind, a.value, a.state, relativeTime(now, a.lastSeen)))

		// The slice is read by index, so the order comes from the sorted query
		// and never from map iteration.
		observed := byAsset[a.id]
		if len(observed) == 0 {
			continue
		}
		rendered := make([]string, 0, briefingServicesPerAsset)
		for i, s := range observed {
			if i == briefingServicesPerAsset {
				rendered = append(rendered, fmt.Sprintf("+%d more %s", len(observed)-i, truncatedMarker))
				break
			}
			rendered = append(rendered, formatService(s))
		}
		lines = append(lines, "  services: "+strings.Join(rendered, ", "))
	}
	if len(lines) == 0 {
		lines = []string{"(none)"}
	}
	return briefingSection{title: title, lines: lines}
}

// formatService renders one service, omitting unknown parts.
func formatService(s serviceRow) string {
	parts := []string{fmt.Sprintf("%d/%s", s.port, s.proto)}
	for _, value := range []string{s.service, s.product, s.version} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}
