package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrInvalidAttackGraph is returned when a node or edge violates a column
// contract before it reaches SQL.
var ErrInvalidAttackGraph = errors.New("store: invalid attack graph element")

var attackNodeKinds = []string{
	"asset", "service", "credential", "session", "vuln", "goal", "data",
}

var attackEdgeKinds = []string{
	"can_reach", "exploits", "yields", "authenticates_to", "pivots_to", "exfiltrates",
}

// IsAttackNodeKind reports whether kind satisfies attack_nodes.kind.
func IsAttackNodeKind(kind string) bool { return slices.Contains(attackNodeKinds, kind) }

// IsAttackEdgeKind reports whether kind satisfies attack_edges.kind.
func IsAttackEdgeKind(kind string) bool { return slices.Contains(attackEdgeKinds, kind) }

// AttackNode is one row of the attack graph's node set.
type AttackNode struct {
	ID           domain.ID
	EngagementID domain.ID
	AssetID      domain.ID
	NodeKey      string
	Kind         string
	Label        string
	AttrsJSON    string
	Reachable    bool
	Priority     float64
	CreatedAt    domain.TimestampMs
	UpdatedAt    domain.TimestampMs
}

// AttackEdge is one row of the attack graph's edge set.
type AttackEdge struct {
	ID           domain.ID
	EngagementID domain.ID
	FromNodeID   domain.ID
	ToNodeID     domain.ID
	Kind         string
	Technique    string
	Cost         float64
	Confidence   float64
	FindingID    domain.ID
	CreatedAt    domain.TimestampMs
}

// AttackRepo reads and writes the mapper's graph.
type AttackRepo struct{ db *DB }

// NewAttackRepo returns a repository over the shared database handle.
func NewAttackRepo(db *DB) *AttackRepo { return &AttackRepo{db: db} }

const attackNodeColumns = `id, engagement_id, asset_id, node_key, kind, label,
	attrs_json, reachable, priority, created_at, updated_at`

const attackEdgeColumns = `id, engagement_id, from_node_id, to_node_id, kind,
	technique, cost, confidence, finding_id, created_at`

const upsertNodeSQL = `INSERT INTO attack_nodes (` + attackNodeColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(engagement_id, node_key) DO UPDATE SET
		asset_id = excluded.asset_id, label = excluded.label,
		attrs_json = excluded.attrs_json, reachable = excluded.reachable,
		priority = excluded.priority, updated_at = excluded.updated_at
	RETURNING ` + attackNodeColumns

const upsertEdgeSQL = `INSERT INTO attack_edges (` + attackEdgeColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(engagement_id, from_node_id, to_node_id, kind) DO UPDATE SET
		technique = excluded.technique, cost = excluded.cost,
		confidence = excluded.confidence, finding_id = excluded.finding_id
	RETURNING ` + attackEdgeColumns

// UpsertNode persists a node keyed by (engagement_id, node_key).
//
// node_key is the mapper's stable identity for a graph element, so a second
// mapping pass refreshes priority and reachability instead of duplicating the
// graph. The canonical id and created_at survive so existing edges stay valid.
func (r *AttackRepo) UpsertNode(ctx context.Context, node AttackNode) (AttackNode, error) {
	if node.EngagementID.IsZero() {
		return AttackNode{}, fmt.Errorf("%w: node engagement_id is empty", ErrInvalidAttackGraph)
	}
	switch {
	case node.NodeKey == "":
		return AttackNode{}, fmt.Errorf("%w: node_key is empty", ErrInvalidAttackGraph)
	case node.Label == "":
		return AttackNode{}, fmt.Errorf("%w: node %q label is empty", ErrInvalidAttackGraph, node.NodeKey)
	case !IsAttackNodeKind(node.Kind):
		return AttackNode{}, fmt.Errorf("%w: node kind %q", ErrInvalidAttackGraph, node.Kind)
	case node.UpdatedAt.IsZero():
		return AttackNode{}, fmt.Errorf("%w: node updated_at is empty", ErrInvalidAttackGraph)
	}
	if node.CreatedAt.IsZero() {
		node.CreatedAt = node.UpdatedAt
	}
	if node.ID.IsZero() {
		node.ID = rowIDMint.New()
	}
	if node.AttrsJSON != "" && !json.Valid([]byte(node.AttrsJSON)) {
		return AttackNode{}, fmt.Errorf("%w: node %q attrs_json is not valid JSON",
			ErrInvalidAttackGraph, node.NodeKey)
	}
	row := r.db.Writer().QueryRowContext(ctx, upsertNodeSQL,
		node.ID.String(), node.EngagementID.String(), nullID(node.AssetID),
		node.NodeKey, node.Kind, node.Label, nullString(node.AttrsJSON),
		boolToInt(node.Reachable), node.Priority,
		int64(node.CreatedAt), int64(node.UpdatedAt))
	stored, err := scanAttackNode(row)
	if err != nil {
		return AttackNode{}, fmt.Errorf("upsert attack node %q: %w", node.NodeKey, err)
	}
	return stored, nil
}

// UpsertEdge persists an edge keyed by (engagement_id, from, to, kind).
func (r *AttackRepo) UpsertEdge(ctx context.Context, edge AttackEdge) (AttackEdge, error) {
	if edge.EngagementID.IsZero() {
		return AttackEdge{}, fmt.Errorf("%w: edge engagement_id is empty", ErrInvalidAttackGraph)
	}
	if edge.FromNodeID.IsZero() || edge.ToNodeID.IsZero() {
		return AttackEdge{}, fmt.Errorf("%w: edge endpoints must be set", ErrInvalidAttackGraph)
	}
	if !IsAttackEdgeKind(edge.Kind) {
		return AttackEdge{}, fmt.Errorf("%w: edge kind %q", ErrInvalidAttackGraph, edge.Kind)
	}
	if edge.CreatedAt.IsZero() {
		return AttackEdge{}, fmt.Errorf("%w: edge created_at is empty", ErrInvalidAttackGraph)
	}
	if edge.Cost <= 0 {
		edge.Cost = 1
	}
	if edge.Confidence <= 0 {
		edge.Confidence = 0.5
	}
	if edge.Confidence > 1 {
		return AttackEdge{}, fmt.Errorf("%w: edge confidence %v is above 1",
			ErrInvalidAttackGraph, edge.Confidence)
	}
	if edge.ID.IsZero() {
		edge.ID = rowIDMint.New()
	}
	row := r.db.Writer().QueryRowContext(ctx, upsertEdgeSQL,
		edge.ID.String(), edge.EngagementID.String(), edge.FromNodeID.String(),
		edge.ToNodeID.String(), edge.Kind, nullString(edge.Technique),
		edge.Cost, edge.Confidence, nullID(edge.FindingID), int64(edge.CreatedAt))
	stored, err := scanAttackEdge(row)
	if err != nil {
		return AttackEdge{}, fmt.Errorf("upsert attack edge %s: %w", edge.Kind, err)
	}
	return stored, nil
}

// ListNodes returns an engagement's nodes ordered by node_key.
//
// node_key, not insertion order, is the ranking input: two mapping passes over
// the same database must produce the same sequence.
func (r *AttackRepo) ListNodes(ctx context.Context, engagementID domain.ID) ([]AttackNode, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("%w: engagement id is empty", ErrInvalidAttackGraph)
	}
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+attackNodeColumns+`
		FROM attack_nodes WHERE engagement_id = ?
		ORDER BY node_key ASC, id ASC`, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list attack nodes for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	return queryAll(rows, scanAttackNode, "attack node")
}

// ListEdges returns an engagement's edges ordered by kind then endpoints.
func (r *AttackRepo) ListEdges(ctx context.Context, engagementID domain.ID) ([]AttackEdge, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("%w: engagement id is empty", ErrInvalidAttackGraph)
	}
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+attackEdgeColumns+`
		FROM attack_edges WHERE engagement_id = ?
		ORDER BY kind ASC, from_node_id ASC, to_node_id ASC, id ASC`, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list attack edges for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	return queryAll(rows, scanAttackEdge, "attack edge")
}

// CountNodes returns how many nodes an engagement has.
func (r *AttackRepo) CountNodes(ctx context.Context, engagementID domain.ID) (int, error) {
	return r.count(ctx, `SELECT COUNT(*) FROM attack_nodes WHERE engagement_id = ?`,
		"attack nodes", engagementID)
}

// CountEdges returns how many edges an engagement has.
func (r *AttackRepo) CountEdges(ctx context.Context, engagementID domain.ID) (int, error) {
	return r.count(ctx, `SELECT COUNT(*) FROM attack_edges WHERE engagement_id = ?`,
		"attack edges", engagementID)
}

func (r *AttackRepo) count(ctx context.Context, query, what string, engagementID domain.ID) (int, error) {
	if engagementID.IsZero() {
		return 0, fmt.Errorf("%w: engagement id is empty", ErrInvalidAttackGraph)
	}
	var total int
	if err := r.db.Reader().QueryRowContext(ctx, query, engagementID.String()).Scan(&total); err != nil {
		return 0, fmt.Errorf("count %s for engagement %q: %w", what, engagementID, err)
	}
	return total, nil
}

func scanAttackNode(scanner rowScanner) (AttackNode, error) {
	var (
		node      AttackNode
		id, engID string
		assetID   *string
		attrsJSON *string
		reachable int
		createdAt int64
		updatedAt int64
	)
	if err := scanner.Scan(&id, &engID, &assetID, &node.NodeKey, &node.Kind,
		&node.Label, &attrsJSON, &reachable, &node.Priority,
		&createdAt, &updatedAt); err != nil {
		return AttackNode{}, err
	}
	var err error
	if node.ID, err = domain.NewID(id); err != nil {
		return AttackNode{}, fmt.Errorf("attack node id: %w", err)
	}
	if node.EngagementID, err = domain.NewID(engID); err != nil {
		return AttackNode{}, fmt.Errorf("attack node engagement id: %w", err)
	}
	node.AttrsJSON = derefString(attrsJSON)
	node.Reachable = reachable == 1
	node.CreatedAt = domain.TimestampMs(createdAt)
	node.UpdatedAt = domain.TimestampMs(updatedAt)
	if assetID != nil && *assetID != "" {
		if node.AssetID, err = domain.NewID(*assetID); err != nil {
			return AttackNode{}, fmt.Errorf("attack node asset id: %w", err)
		}
	}
	return node, nil
}

func scanAttackEdge(scanner rowScanner) (AttackEdge, error) {
	var (
		edge         AttackEdge
		id, engID    string
		fromID, toID string
		technique    *string
		findingID    *string
		createdAt    int64
	)
	if err := scanner.Scan(&id, &engID, &fromID, &toID, &edge.Kind, &technique,
		&edge.Cost, &edge.Confidence, &findingID, &createdAt); err != nil {
		return AttackEdge{}, err
	}
	var err error
	if edge.ID, err = domain.NewID(id); err != nil {
		return AttackEdge{}, fmt.Errorf("attack edge id: %w", err)
	}
	if edge.EngagementID, err = domain.NewID(engID); err != nil {
		return AttackEdge{}, fmt.Errorf("attack edge engagement id: %w", err)
	}
	if edge.FromNodeID, err = domain.NewID(fromID); err != nil {
		return AttackEdge{}, fmt.Errorf("attack edge from id: %w", err)
	}
	if edge.ToNodeID, err = domain.NewID(toID); err != nil {
		return AttackEdge{}, fmt.Errorf("attack edge to id: %w", err)
	}
	edge.Technique = derefString(technique)
	edge.CreatedAt = domain.TimestampMs(createdAt)
	if findingID != nil && *findingID != "" {
		if edge.FindingID, err = domain.NewID(*findingID); err != nil {
			return AttackEdge{}, fmt.Errorf("attack edge finding id: %w", err)
		}
	}
	return edge, nil
}
