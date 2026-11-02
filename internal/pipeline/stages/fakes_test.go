package stages_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/store"
)

const refNow = domain.TimestampMs(1_760_000_000_000)

type fixedClock struct{}

func (fixedClock) Now() domain.TimestampMs { return refNow }

// fakeAssets is an in-memory AssetRecorder.
type fakeAssets struct {
	assets []store.Asset
	nextID domain.IDGenerator
}

func newFakeAssets() *fakeAssets {
	return &fakeAssets{nextID: domain.NewULIDGenerator()}
}

func (f *fakeAssets) UpsertAsset(_ context.Context, asset store.Asset) (store.Asset, error) {
	for i, existing := range f.assets {
		if existing.Kind == asset.Kind && existing.Value == asset.Value {
			asset.ID = existing.ID
			asset.FirstSeenAt = existing.FirstSeenAt
			f.assets[i] = asset
			return asset, nil
		}
	}
	if asset.ID.IsZero() {
		asset.ID = f.nextID.New()
	}
	f.assets = append(f.assets, asset)
	return asset, nil
}

func (f *fakeAssets) ListAssets(context.Context, domain.ID) ([]store.Asset, error) {
	out := make([]store.Asset, len(f.assets))
	copy(out, f.assets)
	return out, nil
}

func (f *fakeAssets) CountAssets(_ context.Context, _ domain.ID, kind string) (int, error) {
	if kind == "" {
		return len(f.assets), nil
	}
	total := 0
	for _, asset := range f.assets {
		if asset.Kind == kind {
			total++
		}
	}
	return total, nil
}

// fakeServices is an in-memory ServiceLister.
type fakeServices struct {
	services []store.Service
}

func (f *fakeServices) ListServices(context.Context, domain.ID) ([]store.Service, error) {
	return f.services, nil
}

// fakeFindings is an in-memory FindingRecorder.
type fakeFindings struct {
	findings []store.Finding
	nextID   domain.IDGenerator
}

func newFakeFindings() *fakeFindings {
	return &fakeFindings{nextID: domain.NewULIDGenerator()}
}

func (f *fakeFindings) CreateFinding(_ context.Context, finding store.Finding) (store.Finding, error) {
	if finding.ID.IsZero() {
		finding.ID = f.nextID.New()
	}
	f.findings = append(f.findings, finding)
	return finding, nil
}

func (f *fakeFindings) ListFindings(context.Context, domain.ID) ([]store.Finding, error) {
	out := make([]store.Finding, len(f.findings))
	copy(out, f.findings)
	return out, nil
}

func (f *fakeFindings) CountFindings(context.Context, domain.ID) (int, error) {
	return len(f.findings), nil
}

// fakeGraph is an in-memory AttackRecorder that records call order.
type fakeGraph struct {
	nodes     map[string]store.AttackNode
	edges     []store.AttackEdge
	nextID    domain.IDGenerator
	nodeOrder []string
	edgeOrder []string
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{
		nodes:  make(map[string]store.AttackNode),
		nextID: domain.NewULIDGenerator(),
	}
}

func (f *fakeGraph) UpsertNode(_ context.Context, node store.AttackNode) (store.AttackNode, error) {
	f.nodeOrder = append(f.nodeOrder, node.NodeKey)
	if existing, ok := f.nodes[node.NodeKey]; ok {
		node.ID = existing.ID
		node.CreatedAt = existing.CreatedAt
		f.nodes[node.NodeKey] = node
		return node, nil
	}
	node.ID = f.nextID.New()
	f.nodes[node.NodeKey] = node
	return node, nil
}

func (f *fakeGraph) UpsertEdge(_ context.Context, edge store.AttackEdge) (store.AttackEdge, error) {
	f.edgeOrder = append(f.edgeOrder, edge.Kind+":"+edge.FromNodeID.String()+">"+edge.ToNodeID.String())
	for _, existing := range f.edges {
		if existing.FromNodeID.Equal(edge.FromNodeID) &&
			existing.ToNodeID.Equal(edge.ToNodeID) && existing.Kind == edge.Kind {
			return existing, nil
		}
	}
	if edge.ID.IsZero() {
		edge.ID = f.nextID.New()
	}
	f.edges = append(f.edges, edge)
	return edge, nil
}

func (f *fakeGraph) ListNodes(context.Context, domain.ID) ([]store.AttackNode, error) {
	out := make([]store.AttackNode, 0, len(f.nodes))
	for _, node := range f.nodes {
		out = append(out, node)
	}
	return out, nil
}

func (f *fakeGraph) ListEdges(context.Context, domain.ID) ([]store.AttackEdge, error) {
	return f.edges, nil
}

func (f *fakeGraph) CountNodes(context.Context, domain.ID) (int, error) { return len(f.nodes), nil }

func (f *fakeGraph) CountEdges(context.Context, domain.ID) (int, error) { return len(f.edges), nil }

// stateFixture builds a pipeline state for an engagement.
func stateFixture(t *testing.T) *pipeline.State {
	t.Helper()

	ids := domain.NewULIDGenerator()
	return &pipeline.State{
		Engagement: domain.Engagement{ID: ids.NewAt(refNow), Name: "test-engagement"},
		Run:        pipeline.StageRunRef{ID: ids.NewAt(refNow), Stage: domain.StageOSINT, Attempt: 1},
		Clock:      fixedClock{},
	}
}

// failingSource always errors and counts its calls.
type failingSource struct {
	name string
	call int
}

func (s *failingSource) Name() string { return s.name }

func (s *failingSource) Query(context.Context, string) ([]osint.Record, error) {
	s.call++
	return nil, errors.New("upstream unavailable")
}

// workingSource returns one well-formed subdomain record.
type workingSource struct{ name string }

func (s workingSource) Name() string { return s.name }

func (s workingSource) Query(_ context.Context, target string) ([]osint.Record, error) {
	return []osint.Record{{
		Kind:       "subdomain",
		Value:      "api." + target,
		Source:     s.name,
		Confidence: 0.9,
	}}, nil
}
