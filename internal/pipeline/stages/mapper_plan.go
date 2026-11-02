package stages

import (
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// planNodes builds the node plan in deterministic node_key order.
func planNodes(assets []store.Asset, findings []store.Finding) []store.AttackNode {
	nodes := make([]store.AttackNode, 0, len(assets)+len(findings))

	for _, asset := range assets {
		nodes = append(nodes, store.AttackNode{
			EngagementID: asset.EngagementID,
			AssetID:      asset.ID,
			NodeKey:      assetNodeKey(asset),
			Kind:         "asset",
			Label:        displayOf(asset),
			Reachable:    true,
			Priority:     assetPriority(asset),
			UpdatedAt:    asset.LastSeenAt,
			CreatedAt:    asset.FirstSeenAt,
		})
	}

	for _, finding := range findings {
		nodes = append(nodes, store.AttackNode{
			EngagementID: finding.EngagementID,
			AssetID:      finding.AssetID,
			NodeKey:      vulnNodeKey(finding),
			Kind:         "vuln",
			Label:        finding.Title,
			Reachable:    true,
			Priority:     severityPriority(finding.Severity),
			UpdatedAt:    finding.UpdatedAt,
			CreatedAt:    finding.CreatedAt,
		})
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeKey < nodes[j].NodeKey })
	return nodes
}

// planEdges builds the edge plan in deterministic (kind, from, to) order.
func planEdges(
	engagementID domain.ID,
	assets []store.Asset,
	findings []store.Finding,
	assetNodes map[string]store.AttackNode,
	vulnNodes map[string]store.AttackNode,
	now domain.TimestampMs,
) []store.AttackEdge {
	edges := make([]store.AttackEdge, 0, len(findings)+len(assets))

	for _, finding := range findings {
		if finding.AssetID.IsZero() {
			continue
		}
		asset, ok := assetByID(assets, finding.AssetID)
		if !ok {
			continue
		}
		from, okFrom := vulnNodes[vulnNodeKey(finding)]
		to, okTo := assetNodes[assetNodeKey(asset)]
		if !okFrom || !okTo {
			continue
		}
		edges = append(edges, store.AttackEdge{
			EngagementID: engagementID,
			FromNodeID:   from.ID,
			ToNodeID:     to.ID,
			Kind:         "exploits",
			Cost:         1.0,
			Confidence:   confidenceForFinding(finding),
			FindingID:    finding.ID,
			CreatedAt:    now,
		})
	}

	edges = append(edges, reachabilityEdges(engagementID, assets, assetNodes, now)...)

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		if edges[i].FromNodeID != edges[j].FromNodeID {
			return edges[i].FromNodeID.String() < edges[j].FromNodeID.String()
		}
		return edges[i].ToNodeID.String() < edges[j].ToNodeID.String()
	})
	return edges
}

// reachabilityEdges links consecutive assets that share a site key.
//
// Consecutive rather than complete: a site with n assets would otherwise produce
// nÂ² edges of the same information, and the graph's purpose is to show a path,
// not to restate membership.
func reachabilityEdges(
	engagementID domain.ID,
	assets []store.Asset,
	assetNodes map[string]store.AttackNode,
	now domain.TimestampMs,
) []store.AttackEdge {
	groups := make(map[string][]store.Asset, len(assets))
	for _, asset := range assets {
		key := siteKey(asset)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], asset)
	}

	edges := make([]store.AttackEdge, 0, len(assets))
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return assetNodeKey(group[i]) < assetNodeKey(group[j])
		})
		for i := 0; i+1 < len(group); i++ {
			from, okFrom := assetNodes[assetNodeKey(group[i])]
			to, okTo := assetNodes[assetNodeKey(group[i+1])]
			if !okFrom || !okTo {
				continue
			}
			edges = append(edges, store.AttackEdge{
				EngagementID: engagementID,
				FromNodeID:   from.ID,
				ToNodeID:     to.ID,
				Kind:         "can_reach",
				Cost:         1.0,
				Confidence:   0.4,
				CreatedAt:    now,
			})
		}
	}
	return edges
}

// assetNodeKey is an asset's stable graph identity.
func assetNodeKey(asset store.Asset) string {
	return "asset:" + asset.Kind + ":" + asset.Value
}

// vulnNodeKey is a finding's stable graph identity.
func vulnNodeKey(finding store.Finding) string {
	return "vuln:" + finding.ID.String()
}

// siteKey groups assets that belong to one registered domain.
func siteKey(asset store.Asset) string {
	host := asset.Value
	if asset.Kind == assetKindURL || asset.Kind == assetKindEndpoint || asset.Kind == assetKindAPI {
		host = hostOf(asset.Value)
	}
	host = strings.TrimPrefix(strings.ToLower(host), "*.")
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return ""
	}
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 && len(labels[len(labels)-2]) <= 3 {
		return strings.Join(labels[len(labels)-3:], ".")
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// hostOf extracts the authority from an absolute URL value.
func hostOf(value string) string {
	lowered := strings.ToLower(value)
	for _, scheme := range []string{"https://", "http://"} {
		if !strings.HasPrefix(lowered, scheme) {
			continue
		}
		rest := value[len(scheme):]
		if end := strings.IndexAny(rest, "/?#"); end >= 0 {
			rest = rest[:end]
		}
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if colon := strings.LastIndex(rest, ":"); colon >= 0 {
			rest = rest[:colon]
		}
		return rest
	}
	return ""
}

// assetByID finds one asset by identifier.
func assetByID(assets []store.Asset, id domain.ID) (store.Asset, bool) {
	for _, asset := range assets {
		if asset.ID.Equal(id) {
			return asset, true
		}
	}
	return store.Asset{}, false
}

// assetPriority ranks an asset node by how strongly it is attested.
func assetPriority(asset store.Asset) float64 {
	priority := asset.Confidence * 0.5
	if asset.State == "alive" {
		priority += 0.2
	}
	return priority
}

// severityPriority ranks a vulnerability node by severity.
func severityPriority(severity string) float64 {
	switch severity {
	case "critical":
		return 1.0
	case "high":
		return 0.8
	case "medium":
		return 0.6
	case "low":
		return 0.4
	case "info":
		return 0.2
	}
	return 0.1
}

// confidenceForFinding maps a finding's qualitative confidence to an edge weight.
func confidenceForFinding(finding store.Finding) float64 {
	switch finding.Confidence {
	case "confirmed":
		return 0.9
	case "likely":
		return 0.7
	case "possible":
		return 0.5
	}
	return 0.5
}
