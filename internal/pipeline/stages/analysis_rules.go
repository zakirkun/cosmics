package stages

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/store"
)

// hypothesis is one deterministic finding candidate.
type hypothesis struct {
	Title       string
	Category    string
	Severity    string
	Confidence  string
	Description string
	AssetID     domain.ID
	Endpoint    string
}

// toFinding renders a candidate against the stage's persisted context.
func (h hypothesis) toFinding(state *pipeline.State, now domain.TimestampMs) store.Finding {
	finding := store.Finding{
		EngagementID: state.Engagement.ID,
		StageRunID:   state.Run.ID,
		Title:        h.Title,
		Category:     h.Category,
		Severity:     h.Severity,
		Confidence:   h.Confidence,
		Description:  h.Description,
		Status:       "open",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if !h.AssetID.IsZero() {
		finding.AssetID = h.AssetID
	}
	return finding
}

// deriveHypotheses applies the deterministic rules to persisted rows.
func deriveHypotheses(assets []store.Asset, services []store.Service) []hypothesis {
	hypotheses := make([]hypothesis, 0, len(assets)+len(services))

	for _, asset := range assets {
		hypotheses = append(hypotheses, assetHypotheses(asset)...)
	}

	byAsset := make(map[string]store.Asset, len(assets))
	for _, asset := range assets {
		byAsset[asset.ID.String()] = asset
	}
	for _, service := range services {
		asset, ok := byAsset[service.AssetID.String()]
		if !ok {
			continue
		}
		hypotheses = append(hypotheses, serviceHypotheses(asset, service)...)
	}
	return hypotheses
}

// assetHypotheses applies the rules that need only the asset itself.
func assetHypotheses(asset store.Asset) []hypothesis {
	value := strings.ToLower(asset.Value)
	label := displayOf(asset)

	switch asset.Kind {
	case assetKindURL, assetKindAPI, assetKindEndpoint:
		if signal := exposureSignal(value); signal != "" {
			return []hypothesis{{
				Title:       fmt.Sprintf("Potential data exposure at %s", label),
				Category:    "data_exposure",
				Severity:    "high",
				Confidence:  "possible",
				Description: fmt.Sprintf("The path of %s contains %q, a common location for backups, VCS metadata, or environment files. Confirm reachability and whether the response is intended to be public.", label, signal),
				AssetID:     asset.ID,
				Endpoint:    asset.Value,
			}}
		}
		if authSignal(value) {
			return []hypothesis{{
				Title:       fmt.Sprintf("Authentication surface at %s", label),
				Category:    "auth",
				Severity:    "medium",
				Confidence:  "possible",
				Description: fmt.Sprintf("%s exposes a sign-in or administrative path. Review authentication controls, lockout behaviour, and default accounts.", label),
				AssetID:     asset.ID,
				Endpoint:    asset.Value,
			}}
		}
		return []hypothesis{{
			Title:       fmt.Sprintf("Web surface at %s", label),
			Category:    "web",
			Severity:    "info",
			Confidence:  "possible",
			Description: fmt.Sprintf("%s is an HTTP-reachable asset. Record the technology stack and the endpoints beneath it before testing input handling.", label),
			AssetID:     asset.ID,
			Endpoint:    asset.Value,
		}}
	case assetKindBucket:
		return []hypothesis{{
			Title:       fmt.Sprintf("Cloud bucket: %s", label),
			Category:    "cloud",
			Severity:    "medium",
			Confidence:  "possible",
			Description: fmt.Sprintf("Object storage %s is associated with the engagement. Check that listing and object reads require credentials.", label),
			AssetID:     asset.ID,
		}}
	case assetKindMobileApp:
		return []hypothesis{{
			Title:       fmt.Sprintf("Mobile application: %s", label),
			Category:    "mobile",
			Severity:    "info",
			Confidence:  "possible",
			Description: fmt.Sprintf("Mobile artifact %s is in scope. Extract transport security, storage, and authentication behaviour from the package.", label),
			AssetID:     asset.ID,
		}}
	}
	return nil
}

// serviceHypotheses applies the rules that need an observed service.
func serviceHypotheses(asset store.Asset, service store.Service) []hypothesis {
	label := fmt.Sprintf("%s:%d/%s", displayOf(asset), service.Port, service.Protocol)

	if isWebPort(service.Port) {
		return []hypothesis{{
			Title:       fmt.Sprintf("Web service on %s", label),
			Category:    "web",
			Severity:    "info",
			Confidence:  "likely",
			Description: fmt.Sprintf("%s is open%s. Capture the banner and TLS configuration, then enumerate paths.", label, productSuffix(service)),
			AssetID:     asset.ID,
			Endpoint:    label,
		}}
	}
	if service.State == "open" {
		return []hypothesis{{
			Title:       fmt.Sprintf("Non-standard exposed service on %s", label),
			Category:    "network",
			Severity:    "low",
			Confidence:  "likely",
			Description: fmt.Sprintf("%s is open%s and is not a conventional web port. Confirm the service is required and version-current.", label, productSuffix(service)),
			AssetID:     asset.ID,
			Endpoint:    label,
		}}
	}
	return nil
}

// displayOf prefers the human-facing value over the canonical one.
func displayOf(asset store.Asset) string {
	if strings.TrimSpace(asset.Display) != "" {
		return asset.Display
	}
	return asset.Value
}

// exposureSignal reports the first data-exposure path marker in a URL.
func exposureSignal(value string) string {
	for _, marker := range []string{
		"/.git", "/.env", "/.svn", "/backup", "/dump", "/export", ".bak", ".sql", "/phpinfo",
	} {
		if strings.Contains(value, marker) {
			return marker
		}
	}
	return ""
}

// authSignal reports whether a URL path looks like an authentication surface.
func authSignal(value string) bool {
	for _, marker := range []string{"/login", "/signin", "/admin", "/auth", "/sso", "/oauth"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// isWebPort reports whether a port conventionally speaks HTTP.
func isWebPort(port int) bool {
	switch port {
	case 80, 443, 8000, 8080, 8443, 8888:
		return true
	}
	return false
}

// productSuffix renders the observed product and version, or nothing.
func productSuffix(service store.Service) string {
	parts := make([]string, 0, 2)
	if service.Service != "" {
		parts = append(parts, service.Service)
	}
	if service.Product != "" {
		product := service.Product
		if service.Version != "" {
			product += " " + service.Version
		}
		parts = append(parts, product)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// rankHypotheses orders candidates most severe first, then by title.
func rankHypotheses(hypotheses []hypothesis) []hypothesis {
	rank := map[string]int{"critical": 1, "high": 2, "medium": 3, "low": 4, "info": 5}

	sort.SliceStable(hypotheses, func(i, j int) bool {
		ri, rj := rank[hypotheses[i].Severity], rank[hypotheses[j].Severity]
		if ri != rj {
			return ri < rj
		}
		return hypotheses[i].Title < hypotheses[j].Title
	})
	return hypotheses
}
