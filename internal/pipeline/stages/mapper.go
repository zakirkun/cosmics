package stages

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/store"
)

// ErrNoMapperInput is returned when the mapper has no asset to graph.
var ErrNoMapperInput = errors.New("stages: mapper requires at least one asset")

// MapperDeps is everything the Mapper stage needs.
type MapperDeps struct {
	Assets   AssetRecorder
	Findings FindingRecorder
	Graph    AttackRecorder
	Clock    domain.Clock
}

// MapperStage builds the attack graph from persisted assets and findings.
type MapperStage struct {
	deps MapperDeps
}

// NewMapperStage validates the dependencies and returns the stage.
func NewMapperStage(deps MapperDeps) (*MapperStage, error) {
	if deps.Assets == nil {
		return nil, fmt.Errorf("mapper stage: asset recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Findings == nil {
		return nil, fmt.Errorf("mapper stage: finding recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Graph == nil {
		return nil, fmt.Errorf("mapper stage: attack recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Clock == nil {
		return nil, fmt.Errorf("mapper stage: clock: %w", pipeline.ErrStageMissing)
	}
	return &MapperStage{deps: deps}, nil
}

// Kind implements pipeline.Stage.
func (s *MapperStage) Kind() domain.StageKind { return domain.StageMapper }

// Enter requires at least one persisted asset.
func (s *MapperStage) Enter(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: mapper state", pipeline.ErrNoState)
	}

	count, err := s.deps.Assets.CountAssets(ctx, state.Engagement.ID, "")
	if err != nil {
		return fmt.Errorf("mapper stage: count assets: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: engagement %s has %d assets; run recon and OSINT first: %w",
			ErrNoMapperInput, state.Engagement.ID, count, ErrMissingPrerequisite)
	}
	return nil
}

// Body writes the node set and then the edge set.
//
// Every write is an upsert keyed by a stable identity, so a retried Body
// refreshes the graph instead of duplicating it. The plan is ordered by
// node_key before anything is written: map iteration order must never reach the
// database, or two runs over the same rows would produce different priorities.
func (s *MapperStage) Body(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: mapper state", pipeline.ErrNoState)
	}

	assets, err := s.deps.Assets.ListAssets(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("mapper stage: list assets: %w", err)
	}
	findings, err := s.deps.Findings.ListFindings(ctx, state.Engagement.ID)
	if err != nil {
		return fmt.Errorf("mapper stage: list findings: %w", err)
	}

	now := s.deps.Clock.Now()
	planned := planNodes(assets, findings)

	assetNodes := make(map[string]store.AttackNode, len(assets))
	vulnNodes := make(map[string]store.AttackNode, len(findings))

	for _, node := range planned {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("mapper stage cancelled: %w", err)
		}
		stored, err := s.deps.Graph.UpsertNode(ctx, node)
		if err != nil {
			return fmt.Errorf("mapper stage: persist node %q: %w", node.NodeKey, err)
		}
		if node.Kind == "vuln" {
			vulnNodes[stored.NodeKey] = stored
		} else {
			assetNodes[stored.NodeKey] = stored
		}
	}

	for _, edge := range planEdges(state.Engagement.ID, assets, findings, assetNodes, vulnNodes, now) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("mapper stage cancelled: %w", err)
		}
		if _, err := s.deps.Graph.UpsertEdge(ctx, edge); err != nil {
			return fmt.Errorf("mapper stage: persist %s edge: %w", edge.Kind, err)
		}
	}
	return nil
}

// Exit requires both a node and an edge.
func (s *MapperStage) Exit(ctx context.Context, state *pipeline.State) pipeline.ExitResult {
	if state == nil {
		return pipeline.Unmet("mapper state is missing")
	}

	nodes, err := s.deps.Graph.CountNodes(ctx, state.Engagement.ID)
	if err != nil {
		return pipeline.Unmet("attack nodes for engagement %s could not be counted: %v", state.Engagement.ID, err)
	}
	edges, err := s.deps.Graph.CountEdges(ctx, state.Engagement.ID)
	if err != nil {
		return pipeline.Unmet("attack edges for engagement %s could not be counted: %v", state.Engagement.ID, err)
	}

	switch {
	case nodes == 0 && edges == 0:
		return pipeline.Unmet("attack graph for engagement %s has no nodes and no edges", state.Engagement.ID)
	case nodes == 0:
		return pipeline.Unmet("attack graph for engagement %s has %d edges but no nodes", state.Engagement.ID, edges)
	case edges == 0:
		return pipeline.Unmet("attack graph for engagement %s has %d nodes but no edges; map a finding to the asset it affects or connect assets under one site", state.Engagement.ID, nodes)
	}
	return pipeline.MetWith()
}
