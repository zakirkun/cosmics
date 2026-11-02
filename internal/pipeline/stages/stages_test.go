package stages_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/pipeline/stages"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_OSINTStage_Enter_fails_when_no_domain_target_exists(t *testing.T) {
	// Given a stage with no target at all
	stage, err := stages.NewOSINTStage(stages.OSINTDeps{
		Assets: newFakeAssets(),
		Clock:  fixedClock{},
	})
	require.NoError(t, err)

	// When
	enterErr := stage.Enter(context.Background(), stateFixture(t))

	// Then the failure names the missing input
	require.Error(t, enterErr)
	require.ErrorIs(t, enterErr, stages.ErrNoOSINTTarget)
	assert.Contains(t, enterErr.Error(), "target")
}

func Test_OSINTStage_Body_continues_when_one_source_fails(t *testing.T) {
	// Given one dead source and one healthy source
	assets := newFakeAssets()
	dead := &failingSource{name: "alpha_dead"}
	stage, err := stages.NewOSINTStage(stages.OSINTDeps{
		Assets:  assets,
		Sources: osint.NewRegistry(dead, workingSource{name: "beta_ok"}),
		Targets: []domain.Target{{Kind: domain.TargetDomain, Value: "example.test"}},
		Clock:   fixedClock{},
	})
	require.NoError(t, err)

	// When
	bodyErr := stage.Body(context.Background(), stateFixture(t))

	// Then the healthy source's work is still persisted
	require.NoError(t, bodyErr, "a dead source must not fail the whole stage")
	assert.Equal(t, 1, dead.call, "the failing source was attempted")
	require.Len(t, assets.assets, 1)
	assert.Equal(t, "api.example.test", assets.assets[0].Value)
	assert.Equal(t, "osint:beta_ok", assets.assets[0].Source)

	// And the gap is reported
	reasons := stage.LastSourceErrors()
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "alpha_dead")
	assert.Contains(t, reasons[0], "upstream unavailable")
}

func Test_OSINTStage_Exit_is_unmet_when_no_asset_was_persisted(t *testing.T) {
	// Given a stage that has persisted nothing
	stage, err := stages.NewOSINTStage(stages.OSINTDeps{
		Assets:  newFakeAssets(),
		Sources: osint.NewRegistry(workingSource{name: "beta_ok"}),
		Targets: []domain.Target{{Kind: domain.TargetDomain, Value: "example.test"}},
		Clock:   fixedClock{},
	})
	require.NoError(t, err)

	// When
	exit := stage.Exit(context.Background(), stateFixture(t))

	// Then the verdict is unmet and the reason names the missing item
	require.False(t, exit.Met)
	assert.Contains(t, exit.Reason, "no OSINT-sourced asset")
	assert.Contains(t, exit.Reason, "beta_ok", "the reason must name the sources that were tried")
}

func Test_AnalysisStage_Enter_fails_when_no_asset_exists(t *testing.T) {
	// Given an engagement with no assets
	stage, err := stages.NewAnalysisStage(stages.AnalysisDeps{
		Assets:   newFakeAssets(),
		Services: &fakeServices{},
		Findings: newFakeFindings(),
		Clock:    fixedClock{},
	})
	require.NoError(t, err)

	// When
	enterErr := stage.Enter(context.Background(), stateFixture(t))

	// Then the error reports the count it found
	require.Error(t, enterErr)
	require.ErrorIs(t, enterErr, stages.ErrNoAnalysisInput)
	assert.Contains(t, enterErr.Error(), "0 assets")
}

func Test_AnalysisStage_Exit_is_unmet_when_no_finding_exists(t *testing.T) {
	// Given assets that produced no finding
	assets := newFakeAssets()
	assets.assets = []store.Asset{{
		ID: domain.NewULIDGenerator().New(), Kind: "domain", Value: "example.test",
	}}
	stage, err := stages.NewAnalysisStage(stages.AnalysisDeps{
		Assets:   assets,
		Services: &fakeServices{},
		Findings: newFakeFindings(),
		Clock:    fixedClock{},
	})
	require.NoError(t, err)

	// When
	exit := stage.Exit(context.Background(), stateFixture(t))

	// Then
	require.False(t, exit.Met)
	assert.Contains(t, exit.Reason, "no finding is persisted")
}

func Test_MapperStage_Exit_requires_both_a_node_and_an_edge(t *testing.T) {
	// Given a mapper over an empty graph
	assets := newFakeAssets()
	assets.assets = []store.Asset{{
		ID: domain.NewULIDGenerator().New(), Kind: "domain", Value: "example.test",
	}}
	graph := newFakeGraph()
	stage, err := stages.NewMapperStage(stages.MapperDeps{
		Assets:   assets,
		Findings: newFakeFindings(),
		Graph:    graph,
		Clock:    fixedClock{},
	})
	require.NoError(t, err)
	state := stateFixture(t)

	// Then neither node nor edge is unmet
	exit := stage.Exit(context.Background(), state)
	require.False(t, exit.Met)
	assert.Contains(t, exit.Reason, "no nodes and no edges")

	// And nodes without edges are unmet, naming what is missing
	graph.nodes["asset:domain:example.test"] = store.AttackNode{NodeKey: "asset:domain:example.test"}
	exit = stage.Exit(context.Background(), state)
	require.False(t, exit.Met)
	assert.Contains(t, exit.Reason, "no edges")

	// And both together satisfy the predicate
	graph.edges = append(graph.edges, store.AttackEdge{Kind: "can_reach"})
	exit = stage.Exit(context.Background(), state)
	assert.True(t, exit.Met)
}

func mapperFixture(t *testing.T) (*fakeAssets, *fakeFindings, *fakeGraph) {
	t.Helper()

	ids := domain.NewULIDGenerator()
	first := ids.NewAt(refNow)
	second := ids.NewAt(refNow + 1)
	findingAsset := ids.NewAt(refNow + 2)

	assets := newFakeAssets()
	assets.assets = []store.Asset{
		{
			ID: first, EngagementID: ids.NewAt(refNow), Kind: "subdomain",
			Value: "a.example.test", Display: "a.example.test", Source: "osint:crtsh",
			Confidence: 0.9, FirstSeenAt: refNow, LastSeenAt: refNow,
		},
		{
			ID: second, EngagementID: ids.NewAt(refNow), Kind: "subdomain",
			Value: "b.example.test", Display: "b.example.test", Source: "osint:crtsh",
			Confidence: 0.9, FirstSeenAt: refNow, LastSeenAt: refNow,
		},
		{
			ID: findingAsset, EngagementID: ids.NewAt(refNow), Kind: "url",
			Value: "https://c.example.test/.git/config", Display: "https://c.example.test/.git/config",
			Source: "osint:wayback", Confidence: 0.6, FirstSeenAt: refNow, LastSeenAt: refNow,
		},
	}

	findings := newFakeFindings()
	findings.findings = []store.Finding{{
		ID: ids.NewAt(refNow + 3), EngagementID: ids.NewAt(refNow), AssetID: findingAsset,
		Title: "Potential data exposure", Category: "data_exposure", Severity: "high",
		Confidence: "possible", Description: "test", Status: "open",
		CreatedAt: refNow, UpdatedAt: refNow,
	}}

	return assets, findings, newFakeGraph()
}

func Test_MapperStage_Body_is_deterministic_when_run_twice_on_the_same_input(t *testing.T) {
	// Given the same persisted input
	assets, findings, graph := mapperFixture(t)
	stage, err := stages.NewMapperStage(stages.MapperDeps{
		Assets: assets, Findings: findings, Graph: graph, Clock: fixedClock{},
	})
	require.NoError(t, err)
	state := stateFixture(t)

	// When the body runs twice
	require.NoError(t, stage.Body(context.Background(), state))
	firstNodes := append([]string(nil), graph.nodeOrder...)
	firstEdges := append([]string(nil), graph.edgeOrder...)

	require.NoError(t, stage.Body(context.Background(), state))
	secondNodes := graph.nodeOrder[len(graph.nodeOrder)-len(firstNodes):]
	secondEdges := graph.edgeOrder[len(graph.edgeOrder)-len(firstEdges):]

	// Then the node_key ordering is identical across runs
	assert.Equal(t, firstNodes, secondNodes, "node_key order must not depend on map iteration")
	assert.Equal(t, firstEdges, secondEdges, "edge order must be identical across runs")
	assert.True(t, strings.HasPrefix(firstNodes[0], "asset:"), "nodes are ordered by node_key")
	for i := 1; i < len(firstNodes); i++ {
		assert.Less(t, firstNodes[i-1], firstNodes[i], "node keys must be ascending")
	}
	assert.True(t, stage.Exit(context.Background(), state).Met)
}
