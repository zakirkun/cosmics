package stages

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
)

// ErrNoAnalysisInput is returned when the analysis stage has no asset to reason
// over: a hypothesis about nothing is not a hypothesis.
var ErrNoAnalysisInput = errors.New("stages: analysis requires at least one asset")

// AnalysisDeps is everything the Analysis stage needs.
type AnalysisDeps struct {
	Assets   AssetRecorder
	Services ServiceLister
	Findings FindingRecorder
	Clock    domain.Clock
}

// AnalysisStage turns persisted assets and services into ranked hypotheses.
//
// This deterministic pass runs first and stands alone on purpose: findings that
// follow mechanically from observed surface must not depend on a model call, and
// a later LLM refinement can only add to what this stage already proved. No LLM
// is called here.
type AnalysisStage struct {
	deps AnalysisDeps
}

// NewAnalysisStage validates the dependencies and returns the stage.
func NewAnalysisStage(deps AnalysisDeps) (*AnalysisStage, error) {
	if deps.Assets == nil {
		return nil, fmt.Errorf("analysis stage: asset recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Services == nil {
		return nil, fmt.Errorf("analysis stage: service lister: %w", pipeline.ErrStageMissing)
	}
	if deps.Findings == nil {
		return nil, fmt.Errorf("analysis stage: finding recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Clock == nil {
		return nil, fmt.Errorf("analysis stage: clock: %w", pipeline.ErrStageMissing)
	}
	return &AnalysisStage{deps: deps}, nil
}

// Kind implements pipeline.Stage.
func (s *AnalysisStage) Kind() domain.StageKind { return domain.StageAnalysis }

// Enter requires at least one persisted asset and names the count it found.
func (s *AnalysisStage) Enter(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: analysis state", pipeline.ErrNoState)
	}

	count, err := s.deps.Assets.CountAssets(ctx, state.Engagement.ID, "")
	if err != nil {
		return fmt.Errorf("analysis stage: count assets: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: engagement %s has %d assets; run recon and OSINT first: %w",
			ErrNoAnalysisInput, state.Engagement.ID, count, ErrMissingPrerequisite)
	}
	return nil
}

// Body derives hypotheses from persisted rows and records the new ones.
//
// Titles already present are skipped, so a retried Body does not duplicate the
// engagement's findings. Ordering is by severity rank then title, never by map
// iteration, which is what makes two runs over the same database identical.
func (s *AnalysisStage) Body(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: analysis state", pipeline.ErrNoState)
	}

	assets, err := s.deps.Assets.ListAssets(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("analysis stage: list assets: %w", err)
	}
	services, err := s.deps.Services.ListServices(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("analysis stage: list services: %w", err)
	}
	existing, err := s.deps.Findings.ListFindings(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("analysis stage: list findings: %w", err)
	}

	known := make(map[string]bool, len(existing))
	for _, finding := range existing {
		known[finding.Title] = true
	}

	candidates := rankHypotheses(deriveHypotheses(assets, services))
	now := s.deps.Clock.Now()

	for _, candidate := range candidates {
		if known[candidate.Title] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("analysis stage cancelled: %w", err)
		}
		if _, err := s.deps.Findings.CreateFinding(ctx, candidate.toFinding(state, now)); err != nil {
			return fmt.Errorf("analysis stage: record hypothesis %q: %w", candidate.Title, err)
		}
		known[candidate.Title] = true
	}
	return nil
}

// Exit is satisfied when at least one finding is persisted.
func (s *AnalysisStage) Exit(ctx context.Context, state *pipeline.State) pipeline.ExitResult {
	if state == nil {
		return pipeline.Unmet("analysis state is missing")
	}

	count, err := s.deps.Findings.CountFindings(ctx, state.Engagement.ID)
	if err != nil {
		return pipeline.Unmet("findings for engagement %s could not be counted: %v", state.Engagement.ID, err)
	}
	if count == 0 {
		return pipeline.Unmet(
			"no finding is persisted for engagement %s: %d assets were examined but produced no hypothesis",
			state.Engagement.ID, countAssetsForReason(ctx, s.deps.Assets, state.Engagement.ID))
	}
	return pipeline.MetWith()
}

// countAssetsForReason reports the asset count for an unmet-exit message.
func countAssetsForReason(ctx context.Context, assets AssetRecorder, engagementID domain.ID) int {
	count, err := assets.CountAssets(ctx, engagementID, "")
	if err != nil {
		return 0
	}
	return count
}
