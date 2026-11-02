package store

import (
	"context"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

const upsertServiceSQL = `INSERT INTO services (
		id, asset_id, port, protocol, state, service, product, version, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(asset_id, port, protocol) DO UPDATE SET
		state = excluded.state, service = excluded.service, product = excluded.product,
		version = excluded.version, updated_at = excluded.updated_at`

// AddService upserts one discovered service on (asset_id, port, protocol).
func (r *AssetRepo) AddService(ctx context.Context, service Service) error {
	if err := validateService(service); err != nil {
		return err
	}
	if service.ID.IsZero() {
		service.ID = rowIDMint.New()
	}
	_, err := r.db.Writer().ExecContext(ctx, upsertServiceSQL,
		service.ID.String(), service.AssetID.String(), service.Port, service.Protocol,
		service.State, nullString(service.Service), nullString(service.Product),
		nullString(service.Version), int64(service.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert %s/%d service for asset %q: %w",
			service.Protocol, service.Port, service.AssetID, err)
	}
	return nil
}

// ListServices returns every service row of an engagement, ordered by asset
// then port.
func (r *AssetRepo) ListServices(ctx context.Context, engagementID domain.ID) ([]Service, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list services: engagement id is empty: %w", ErrInvalidAsset)
	}
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT s.id, s.asset_id, s.port,
			s.protocol, s.state, s.service, s.product, s.version, s.updated_at
		FROM services s JOIN assets a ON a.id = s.asset_id
		WHERE a.engagement_id = ?
		ORDER BY s.asset_id ASC, s.port ASC, s.protocol ASC`, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list services for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	return queryAll(rows, scanService, "service")
}

func validateService(service Service) error {
	if service.AssetID.IsZero() {
		return fmt.Errorf("%w: service asset_id is empty", ErrInvalidAsset)
	}
	if service.Port < 1 || service.Port > 65535 {
		return fmt.Errorf("%w: service port %d is outside 1..65535", ErrInvalidAsset, service.Port)
	}
	if service.Protocol != "tcp" && service.Protocol != "udp" {
		return fmt.Errorf("%w: service protocol %q", ErrInvalidAsset, service.Protocol)
	}
	switch service.State {
	case "", "open", "closed", "filtered", "open|filtered":
	default:
		return fmt.Errorf("%w: service state %q", ErrInvalidAsset, service.State)
	}
	if service.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: service updated_at is empty", ErrInvalidAsset)
	}
	return nil
}

func scanService(scanner rowScanner) (Service, error) {
	var (
		service       Service
		id, assetID   string
		name, product *string
		version       *string
		updatedAt     int64
	)
	if err := scanner.Scan(&id, &assetID, &service.Port, &service.Protocol,
		&service.State, &name, &product, &version, &updatedAt); err != nil {
		return Service{}, err
	}
	var err error
	if service.ID, err = domain.NewID(id); err != nil {
		return Service{}, fmt.Errorf("service id: %w", err)
	}
	if service.AssetID, err = domain.NewID(assetID); err != nil {
		return Service{}, fmt.Errorf("service asset id: %w", err)
	}
	service.Service = derefString(name)
	service.Product = derefString(product)
	service.Version = derefString(version)
	service.UpdatedAt = domain.TimestampMs(updatedAt)
	return service, nil
}
