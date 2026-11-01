package memory

import (
	"context"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// BriefingMaxChars bounds the assembled briefing. A section that would exceed
// the remaining room is cut with truncatedMarker and every later section is
// announced with the same marker, so nothing disappears silently.
const BriefingMaxChars = 12_000

// truncatedMarker labels content removed to satisfy a length cap.
const truncatedMarker = "… (truncated)"

// Section sizes. Both are small because the briefing is a summary, not a dump:
// the agent can retrieve the full rows when it needs them.
const (
	briefingEventLimit       = 10
	briefingServicesPerAsset = 8
	briefingMessageChars     = 160
)

// BriefingDeps carries the state a briefing is rendered from.
type BriefingDeps struct {
	DB         *store.DB
	Engagement domain.Engagement
	Now        domain.TimestampMs
}

// briefingSection is one titled block of already-sorted lines.
type briefingSection struct {
	title string
	lines []string
}

// BuildBriefing synthesises the engagement briefing from the database.
//
// It makes no LLM call: CORE.md section 6.3 requires a deterministic projection
// so the agent's inputs are cheap, reproducible, and auditable. Every list is
// ordered explicitly and no map is iterated for output, so two calls over the
// same rows produce byte-identical text.
func BuildBriefing(ctx context.Context, deps BriefingDeps) (string, error) {
	if deps.DB == nil {
		return "", fmt.Errorf("build briefing: database is nil")
	}
	if deps.Engagement.ID.IsZero() {
		return "", fmt.Errorf("build briefing: engagement id: %w", ErrInvalidFact)
	}

	targets, err := loadTargets(ctx, deps)
	if err != nil {
		return "", err
	}
	assets, services, err := loadAssets(ctx, deps)
	if err != nil {
		return "", err
	}
	facts, superseded, err := loadBriefingFacts(ctx, deps)
	if err != nil {
		return "", err
	}
	findings, err := loadFindings(ctx, deps)
	if err != nil {
		return "", err
	}
	events, err := loadEvents(ctx, deps)
	if err != nil {
		return "", err
	}

	sections := []briefingSection{
		engagementSection(deps),
		targetSection(targets),
		assetSection(assets, services, deps.Now),
		factSection(facts, superseded),
		findingSection(findings),
		eventSection(events, deps.Now),
		budgetSection(deps),
	}
	return assembleBriefing(sections, BriefingMaxChars), nil
}
