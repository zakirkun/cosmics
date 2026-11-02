package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrInvalidAsset is returned when an asset or service row violates a column
// contract before it reaches SQL.
var ErrInvalidAsset = errors.New("store: invalid asset")

// rowIDMint mints identifiers for rows a caller did not pre-assign.
var rowIDMint = domain.NewULIDGenerator()

var assetKinds = []string{
	"domain", "subdomain", "ip", "host", "url",
	"endpoint", "repo", "bucket", "mobile_app", "api",
}

var assetStates = []string{"unknown", "alive", "dead", "filtered"}

// IsAssetKind reports whether kind satisfies the assets CHECK constraint.
func IsAssetKind(kind string) bool { return slices.Contains(assetKinds, kind) }

// Asset is one row of the assets table.
type Asset struct {
	ID           domain.ID
	EngagementID domain.ID
	ParentID     domain.ID
	Kind         string
	Value        string
	Display      string
	State        string
	Confidence   float64
	FirstSeenAt  domain.TimestampMs
	LastSeenAt   domain.TimestampMs
	Source       string
	AttrsJSON    string
	TagsJSON     string
}

// Service is one row of the services table.
type Service struct {
	ID        domain.ID
	AssetID   domain.ID
	Port      int
	Protocol  string
	State     string
	Service   string
	Product   string
	Version   string
	UpdatedAt domain.TimestampMs
}

// AssetRepo reads and writes the discovered surface.
type AssetRepo struct{ db *DB }

// NewAssetRepo returns a repository over the shared database handle.
func NewAssetRepo(db *DB) *AssetRepo { return &AssetRepo{db: db} }

const assetColumns = `id, engagement_id, parent_id, kind, value, display, state,
	confidence, first_seen_at, last_seen_at, source, attrs_json, tags_json`

const upsertAssetSQL = `INSERT INTO assets (` + assetColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(engagement_id, kind, value) DO UPDATE SET
		last_seen_at = excluded.last_seen_at,
		confidence = excluded.confidence,
		state = excluded.state,
		attrs_json = excluded.attrs_json,
		source = CASE WHEN excluded.source LIKE 'osint:%'
			THEN excluded.source ELSE assets.source END
	RETURNING ` + assetColumns

// Upsert persists one asset keyed by (engagement_id, kind, value).
//
// A duplicate observation updates only the volatile columns: the canonical id
// and first_seen_at survive, so foreign keys stay stable. Provenance is
// monotone Ã¢â‚¬â€ an OSINT source replaces a non-OSINT one but never the reverse Ã¢â‚¬â€
// so a later recon re-run cannot erase how the asset was discovered.
func (r *AssetRepo) Upsert(ctx context.Context, asset Asset) (Asset, error) {
	if err := normalizeAsset(&asset); err != nil {
		return Asset{}, err
	}
	row := r.db.Writer().QueryRowContext(ctx, upsertAssetSQL,
		asset.ID.String(), asset.EngagementID.String(), nullID(asset.ParentID),
		asset.Kind, asset.Value, nullString(asset.Display), asset.State,
		asset.Confidence, int64(asset.FirstSeenAt), int64(asset.LastSeenAt),
		asset.Source, asset.AttrsJSON, nullString(asset.TagsJSON))
	stored, err := scanAsset(row)
	if err != nil {
		return Asset{}, fmt.Errorf("upsert asset %s %q: %w", asset.Kind, asset.Value, err)
	}
	return stored, nil
}

// List returns every asset of an engagement sorted by kind then value.
func (r *AssetRepo) List(ctx context.Context, engagementID domain.ID) ([]Asset, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list assets: engagement id is empty: %w", ErrInvalidAsset)
	}
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+assetColumns+`
		FROM assets WHERE engagement_id = ?
		ORDER BY kind ASC, value ASC, id ASC`, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list assets for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	return queryAll(rows, scanAsset, "asset")
}

// CountByKind returns how many assets of one kind an engagement has. An empty
// kind counts every asset.
func (r *AssetRepo) CountByKind(ctx context.Context, engagementID domain.ID, kind string) (int, error) {
	if engagementID.IsZero() {
		return 0, fmt.Errorf("count assets: engagement id is empty: %w", ErrInvalidAsset)
	}
	query := `SELECT COUNT(*) FROM assets WHERE engagement_id = ?`
	args := []any{engagementID.String()}
	if kind != "" {
		if !IsAssetKind(kind) {
			return 0, fmt.Errorf("%w: kind %q", ErrInvalidAsset, kind)
		}
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	var total int
	if err := r.db.Reader().QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count assets for engagement %q: %w", engagementID, err)
	}
	return total, nil
}

// queryAll drains a projection into a slice with one error path.
func queryAll[T any](rows *sql.Rows, scan func(rowScanner) (T, error), label string) ([]T, error) {
	out := make([]T, 0, 32)
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", label, err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %ss: %w", label, err)
	}
	return out, nil
}

// normalizeAsset validates an incoming row and fills safe defaults.
func normalizeAsset(asset *Asset) error {
	if asset.EngagementID.IsZero() {
		return fmt.Errorf("%w: engagement_id is empty", ErrInvalidAsset)
	}
	if !IsAssetKind(asset.Kind) {
		return fmt.Errorf("%w: kind %q", ErrInvalidAsset, asset.Kind)
	}
	if err := validateAssetValue(asset.Kind, asset.Value); err != nil {
		return err
	}
	if asset.State == "" {
		asset.State = "unknown"
	}
	if !slices.Contains(assetStates, asset.State) {
		return fmt.Errorf("%w: state %q", ErrInvalidAsset, asset.State)
	}
	if asset.Confidence == 0 {
		asset.Confidence = 1
	}
	if asset.Confidence < 0 || asset.Confidence > 1 {
		return fmt.Errorf("%w: confidence %v is outside [0,1]", ErrInvalidAsset, asset.Confidence)
	}
	if asset.Source == "" {
		return fmt.Errorf("%w: source is empty", ErrInvalidAsset)
	}
	if asset.LastSeenAt.IsZero() {
		return fmt.Errorf("%w: last_seen_at is empty", ErrInvalidAsset)
	}
	if asset.FirstSeenAt.IsZero() {
		asset.FirstSeenAt = asset.LastSeenAt
	}
	if asset.ID.IsZero() {
		asset.ID = rowIDMint.New()
	}
	if asset.AttrsJSON == "" {
		asset.AttrsJSON = "{}"
	}
	if !json.Valid([]byte(asset.AttrsJSON)) {
		return fmt.Errorf("%w: attrs_json is not valid JSON", ErrInvalidAsset)
	}
	if asset.TagsJSON != "" && !json.Valid([]byte(asset.TagsJSON)) {
		return fmt.Errorf("%w: tags_json is not valid JSON", ErrInvalidAsset)
	}
	return nil
}

// validateAssetValue rejects a value that could reach an argv as hostile data.
func validateAssetValue(kind, value string) error {
	if value == "" || len(value) > 2048 {
		return fmt.Errorf("%w: %s value is empty or too long", ErrInvalidAsset, kind)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s value contains a control character", ErrInvalidAsset, kind)
		}
	}
	if strings.ContainsAny(value, " \t;`|$\\\"'<>") {
		return fmt.Errorf("%w: %s value %q contains whitespace or a shell metacharacter",
			ErrInvalidAsset, kind, value)
	}
	switch kind {
	case "ip":
		if net.ParseIP(value) == nil {
			return fmt.Errorf("%w: ip value %q is not an address", ErrInvalidAsset, value)
		}
	case "domain", "subdomain", "host", "bucket", "repo", "mobile_app":
		for _, r := range value {
			if !isHostnameRune(r) {
				return fmt.Errorf("%w: %s value %q is not hostname-shaped", ErrInvalidAsset, kind, value)
			}
		}
	}
	return nil
}

func isHostnameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '.', r == '-', r == '_', r == '/':
		return true
	}
	return false
}

func scanAsset(scanner rowScanner) (Asset, error) {
	var (
		asset            Asset
		id, engagementID string
		parent           *string
		display, tags    *string
		firstSeen, last  int64
	)
	if err := scanner.Scan(&id, &engagementID, &parent, &asset.Kind, &asset.Value,
		&display, &asset.State, &asset.Confidence, &firstSeen, &last,
		&asset.Source, &asset.AttrsJSON, &tags); err != nil {
		return Asset{}, err
	}
	var err error
	if asset.ID, err = domain.NewID(id); err != nil {
		return Asset{}, fmt.Errorf("asset id: %w", err)
	}
	if asset.EngagementID, err = domain.NewID(engagementID); err != nil {
		return Asset{}, fmt.Errorf("asset engagement id: %w", err)
	}
	asset.Display = derefString(display)
	asset.TagsJSON = derefString(tags)
	asset.FirstSeenAt = domain.TimestampMs(firstSeen)
	asset.LastSeenAt = domain.TimestampMs(last)
	if parent != nil && *parent != "" {
		if asset.ParentID, err = domain.NewID(*parent); err != nil {
			return Asset{}, fmt.Errorf("asset parent id: %w", err)
		}
	}
	return asset, nil
}
