package stages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinel errors returned by the stage helpers.
var (
	// ErrUnknownAssetKind is returned when a record names a kind the assets
	// table does not accept.
	ErrUnknownAssetKind = errors.New("stages: unknown asset kind")

	// ErrUnsafeAssetValue is returned when a value could alter a command line.
	// It is the boundary that stops hostile OSINT data from ever becoming argv.
	ErrUnsafeAssetValue = errors.New("stages: unsafe asset value")

	// ErrInvalidRecord is returned when an OSINT record cannot be normalised.
	ErrInvalidRecord = errors.New("stages: invalid osint record")

	// ErrMissingPrerequisite is returned by Enter when a stage cannot run.
	ErrMissingPrerequisite = errors.New("stages: prerequisite missing")
)

// OSINTSourcePrefix marks the provenance of an asset discovered by an adapter.
// Exit criteria and reporting both key off this prefix.
const OSINTSourcePrefix = "osint:"

const maxAssetValueLength = 2048

const (
	assetKindDomain    = "domain"
	assetKindSubdomain = "subdomain"
	assetKindIP        = "ip"
	assetKindHost      = "host"
	assetKindURL       = "url"
	assetKindEndpoint  = "endpoint"
	assetKindRepo      = "repo"
	assetKindBucket    = "bucket"
	assetKindMobileApp = "mobile_app"
	assetKindAPI       = "api"
)

// AssetRecorder is the persistence the stages need for discovered surface.
type AssetRecorder interface {
	UpsertAsset(ctx context.Context, asset store.Asset) (store.Asset, error)
	ListAssets(ctx context.Context, engagementID domain.ID) ([]store.Asset, error)
	CountAssets(ctx context.Context, engagementID domain.ID, kind string) (int, error)
}

// ServiceLister reads services discovered by recon. It is separate from
// AssetRecorder so a stage that only writes assets does not have to implement it.
type ServiceLister interface {
	ListServices(ctx context.Context, engagementID domain.ID) ([]store.Service, error)
}

// FindingRecorder is the persistence the analysis and mapper stages need.
type FindingRecorder interface {
	CreateFinding(ctx context.Context, finding store.Finding) (store.Finding, error)
	ListFindings(ctx context.Context, engagementID domain.ID) ([]store.Finding, error)
	CountFindings(ctx context.Context, engagementID domain.ID) (int, error)
}

// AttackRecorder is the persistence the mapper stage needs.
type AttackRecorder interface {
	UpsertNode(ctx context.Context, node store.AttackNode) (store.AttackNode, error)
	UpsertEdge(ctx context.Context, edge store.AttackEdge) (store.AttackEdge, error)
	ListNodes(ctx context.Context, engagementID domain.ID) ([]store.AttackNode, error)
	ListEdges(ctx context.Context, engagementID domain.ID) ([]store.AttackEdge, error)
	CountNodes(ctx context.Context, engagementID domain.ID) (int, error)
	CountEdges(ctx context.Context, engagementID domain.ID) (int, error)
}

// AssetStore adapts *store.AssetRepo to AssetRecorder and ServiceLister.
type AssetStore struct {
	repo *store.AssetRepo
}

// NewAssetStore returns an adapter over repo.
func NewAssetStore(repo *store.AssetRepo) *AssetStore { return &AssetStore{repo: repo} }

// UpsertAsset implements AssetRecorder.
func (s *AssetStore) UpsertAsset(ctx context.Context, asset store.Asset) (store.Asset, error) {
	return s.repo.Upsert(ctx, asset)
}

// ListAssets implements AssetRecorder.
func (s *AssetStore) ListAssets(ctx context.Context, engagementID domain.ID) ([]store.Asset, error) {
	return s.repo.List(ctx, engagementID)
}

// CountAssets implements AssetRecorder. An empty kind counts every asset.
func (s *AssetStore) CountAssets(ctx context.Context, engagementID domain.ID, kind string) (int, error) {
	return s.repo.CountByKind(ctx, engagementID, kind)
}

// ListServices implements ServiceLister.
func (s *AssetStore) ListServices(ctx context.Context, engagementID domain.ID) ([]store.Service, error) {
	return s.repo.ListServices(ctx, engagementID)
}

// FindingStore adapts *store.FindingRepo to FindingRecorder.
type FindingStore struct {
	repo *store.FindingRepo
}

// NewFindingStore returns an adapter over repo.
func NewFindingStore(repo *store.FindingRepo) *FindingStore { return &FindingStore{repo: repo} }

// CreateFinding implements FindingRecorder.
func (s *FindingStore) CreateFinding(ctx context.Context, finding store.Finding) (store.Finding, error) {
	return s.repo.Create(ctx, finding)
}

// ListFindings implements FindingRecorder.
func (s *FindingStore) ListFindings(ctx context.Context, engagementID domain.ID) ([]store.Finding, error) {
	return s.repo.List(ctx, engagementID)
}

// CountFindings implements FindingRecorder.
func (s *FindingStore) CountFindings(ctx context.Context, engagementID domain.ID) (int, error) {
	return s.repo.Count(ctx, engagementID)
}

// AttackStore adapts *store.AttackRepo to AttackRecorder.
type AttackStore struct {
	repo *store.AttackRepo
}

// NewAttackStore returns an adapter over repo.
func NewAttackStore(repo *store.AttackRepo) *AttackStore { return &AttackStore{repo: repo} }

// UpsertNode implements AttackRecorder.
func (s *AttackStore) UpsertNode(ctx context.Context, node store.AttackNode) (store.AttackNode, error) {
	return s.repo.UpsertNode(ctx, node)
}

// UpsertEdge implements AttackRecorder.
func (s *AttackStore) UpsertEdge(ctx context.Context, edge store.AttackEdge) (store.AttackEdge, error) {
	return s.repo.UpsertEdge(ctx, edge)
}

// ListNodes implements AttackRecorder.
func (s *AttackStore) ListNodes(ctx context.Context, engagementID domain.ID) ([]store.AttackNode, error) {
	return s.repo.ListNodes(ctx, engagementID)
}

// ListEdges implements AttackRecorder.
func (s *AttackStore) ListEdges(ctx context.Context, engagementID domain.ID) ([]store.AttackEdge, error) {
	return s.repo.ListEdges(ctx, engagementID)
}

// CountNodes implements AttackRecorder.
func (s *AttackStore) CountNodes(ctx context.Context, engagementID domain.ID) (int, error) {
	return s.repo.CountNodes(ctx, engagementID)
}

// CountEdges implements AttackRecorder.
func (s *AttackStore) CountEdges(ctx context.Context, engagementID domain.ID) (int, error) {
	return s.repo.CountEdges(ctx, engagementID)
}

// NormalizeKind maps a source's kind string to a kind the assets table accepts.
func NormalizeKind(raw string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(raw))
	switch kind {
	case assetKindDomain, assetKindSubdomain, assetKindIP, assetKindHost, assetKindURL,
		assetKindEndpoint, assetKindRepo, assetKindBucket, assetKindMobileApp, assetKindAPI:
		return kind, nil
	case "a", "aaaa", "a_record", "aaaa_record", "address":
		return assetKindIP, nil
	case "fqdn", "hostname":
		return assetKindHost, nil
	case "website", "link":
		return assetKindURL, nil
	case "mobileapp", "mobile":
		return assetKindMobileApp, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAssetKind, raw)
}

// NormalizeValue validates and canonicalises one record value for its kind.
//
// A value carrying shell metacharacters is rejected, not escaped: OSINT data is
// untrusted input, and the safe response to hostile data is to drop it before it
// can ever be handed to an executor.
func NormalizeValue(kind, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%w: %s value is empty", ErrUnsafeAssetValue, kind)
	}
	if len(value) > maxAssetValueLength {
		return "", fmt.Errorf("%w: %s value exceeds %d characters", ErrUnsafeAssetValue, kind, maxAssetValueLength)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: %s value contains control character %q", ErrUnsafeAssetValue, kind, r)
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return "", fmt.Errorf("%w: %s value contains whitespace", ErrUnsafeAssetValue, kind)
		}
		if strings.ContainsRune(";`|$\\\"'<>", r) {
			return "", fmt.Errorf("%w: %s value %q contains shell metacharacter %q", ErrUnsafeAssetValue, kind, value, r)
		}
	}

	switch kind {
	case assetKindDomain, assetKindSubdomain, assetKindHost, assetKindBucket:
		host := strings.ToLower(strings.TrimSuffix(value, "."))
		for strings.HasPrefix(host, "*.") {
			host = strings.TrimPrefix(host, "*.")
		}
		if strings.Contains(host, "*") {
			return "", fmt.Errorf("%w: %s value %q contains a wildcard", ErrUnsafeAssetValue, kind, value)
		}
		if err := validateHostname(host); err != nil {
			return "", fmt.Errorf("%w: %s value %q: %w", ErrUnsafeAssetValue, kind, value, err)
		}
		return host, nil
	case assetKindIP:
		parsed := net.ParseIP(value)
		if parsed == nil {
			return "", fmt.Errorf("%w: ip value %q is not an address", ErrUnsafeAssetValue, value)
		}
		return parsed.String(), nil
	case assetKindURL, assetKindEndpoint, assetKindAPI:
		return normalizeURL(kind, value)
	case assetKindRepo, assetKindMobileApp:
		return strings.ToLower(value), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAssetKind, kind)
}

// validateHostname enforces DNS label syntax on a bare hostname.
func validateHostname(host string) error {
	if host == "" {
		return errors.New("hostname is empty")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return errors.New("hostname has an empty label")
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q exceeds 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("label %q starts or ends with a hyphen", label)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return fmt.Errorf("label %q contains %q", label, r)
			}
		}
	}
	return nil
}

// normalizeURL accepts an absolute http(s) URL or an absolute path.
func normalizeURL(kind, value string) (string, error) {
	lowered := strings.ToLower(value)
	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("%w: %s value %q: %w", ErrUnsafeAssetValue, kind, value, err)
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("%w: %s value %q has no host", ErrUnsafeAssetValue, kind, value)
		}
		return parsed.String(), nil
	}
	if strings.HasPrefix(value, "/") {
		return value, nil
	}
	return "", fmt.Errorf("%w: %s value %q is neither an absolute URL nor a path", ErrUnsafeAssetValue, kind, value)
}

// RecordToAsset converts one OSINT record into a store-ready asset.
//
// It does not mint an identifier: the repository owns that, and leaving it unset
// lets the caller test the conversion without a database.
func RecordToAsset(engagementID domain.ID, record osint.Record, now domain.TimestampMs) (store.Asset, error) {
	if engagementID.IsZero() {
		return store.Asset{}, fmt.Errorf("%w: engagement id is empty", ErrInvalidRecord)
	}
	if now.IsZero() {
		return store.Asset{}, fmt.Errorf("%w: observation time is empty", ErrInvalidRecord)
	}

	kind, err := NormalizeKind(record.Kind)
	if err != nil {
		return store.Asset{}, fmt.Errorf("%w: source %q: %w", ErrInvalidRecord, record.Source, err)
	}
	value, err := NormalizeValue(kind, record.Value)
	if err != nil {
		return store.Asset{}, fmt.Errorf("%w: source %q: %w", ErrInvalidRecord, record.Source, err)
	}

	source := strings.TrimSpace(record.Source)
	if source == "" {
		return store.Asset{}, fmt.Errorf("%w: source name is empty", ErrInvalidRecord)
	}

	confidence := record.Confidence
	if confidence <= 0 {
		confidence = 0.5
	}
	if confidence > 1 {
		return store.Asset{}, fmt.Errorf("%w: source %q confidence %v is above 1", ErrInvalidRecord, source, confidence)
	}

	attrsJSON, err := json.Marshal(record.Attrs)
	if err != nil {
		return store.Asset{}, fmt.Errorf("%w: source %q attrs: %w", ErrInvalidRecord, source, err)
	}
	if len(attrsJSON) == 0 || string(attrsJSON) == "null" {
		attrsJSON = []byte("{}")
	}

	return store.Asset{
		EngagementID: engagementID,
		Kind:         kind,
		Value:        value,
		Display:      strings.TrimSpace(record.Value),
		State:        "unknown",
		Confidence:   confidence,
		FirstSeenAt:  now,
		LastSeenAt:   now,
		Source:       OSINTSourcePrefix + source,
		AttrsJSON:    string(attrsJSON),
	}, nil
}
