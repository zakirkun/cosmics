package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// ToolDefRow is one tool_defs row.
//
// It mirrors the embedded registry rather than the other way round: the YAML file
// is the source of truth an operator edits, and the table is its persisted index.
type ToolDefRow struct {
	ID               string
	Category         string
	DisplayName      string
	Image            string
	ImageDigest      string
	Binary           string
	VersionCmd       string
	ArgSchemaJSON    string
	CommandTemplate  string
	CapabilitiesJSON string
	NetworkMode      string
	DefaultTimeoutS  int
	MaxOutputBytes   int
	DangerLevel      string
	RequiresApproval bool
	Enabled          bool
	Notes            string
	UpdatedAt        domain.TimestampMs
}

// ToolDefsRepo persists the tool registry.
type ToolDefsRepo struct {
	db *DB
}

// NewToolDefsRepo returns a repository over the tool_defs table.
func NewToolDefsRepo(db *DB) *ToolDefsRepo {
	return &ToolDefsRepo{db: db}
}

// Sync upserts the given definitions in one transaction and reports how many rows
// changed.
//
// A run references tool_defs through a foreign key, so the table must be populated
// before the first execution. Syncing on startup keeps the table a faithful index
// of the embedded YAML instead of a second definition that can drift.
func (r *ToolDefsRepo) Sync(ctx context.Context, rows []ToolDefRow) (int, error) {
	const query = `
		INSERT INTO tool_defs (
			id, category, display_name, image, image_digest, binary, version_cmd,
			arg_schema_json, command_template, capabilities_json, network_mode,
			default_timeout_s, max_output_bytes, danger_level, requires_approval,
			enabled, notes, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			category = excluded.category,
			display_name = excluded.display_name,
			image = excluded.image,
			image_digest = excluded.image_digest,
			binary = excluded.binary,
			version_cmd = excluded.version_cmd,
			arg_schema_json = excluded.arg_schema_json,
			command_template = excluded.command_template,
			capabilities_json = excluded.capabilities_json,
			network_mode = excluded.network_mode,
			default_timeout_s = excluded.default_timeout_s,
			max_output_bytes = excluded.max_output_bytes,
			danger_level = excluded.danger_level,
			requires_approval = excluded.requires_approval,
			enabled = excluded.enabled,
			notes = excluded.notes,
			updated_at = excluded.updated_at`

	changed := 0

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, row := range rows {
			if err := validateToolDef(row); err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx, query,
				row.ID,
				row.Category,
				row.DisplayName,
				row.Image,
				nullString(row.ImageDigest),
				row.Binary,
				nullString(row.VersionCmd),
				row.ArgSchemaJSON,
				row.CommandTemplate,
				nullString(row.CapabilitiesJSON),
				row.NetworkMode,
				row.DefaultTimeoutS,
				row.MaxOutputBytes,
				row.DangerLevel,
				boolToIntValue(row.RequiresApproval),
				boolToIntValue(row.Enabled),
				nullString(row.Notes),
				int64(row.UpdatedAt),
			); err != nil {
				return fmt.Errorf("upsert tool_defs %s: %w", row.ID, err)
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sync tool defs: %w", err)
	}

	return changed, nil
}

// Get reads one definition by identifier.
func (r *ToolDefsRepo) Get(ctx context.Context, id string) (ToolDefRow, error) {
	const query = `
		SELECT id, category, display_name, image, image_digest, binary, version_cmd,
		       arg_schema_json, command_template, capabilities_json, network_mode,
		       default_timeout_s, max_output_bytes, danger_level, requires_approval,
		       enabled, notes, updated_at
		FROM tool_defs WHERE id = ?`

	var (
		row          ToolDefRow
		imageDigest  sql.NullString
		versionCmd   sql.NullString
		capsJSON     sql.NullString
		notes        sql.NullString
		requiresAppr int
		enabled      int
		updatedAt    int64
	)

	err := r.db.Reader().QueryRowContext(ctx, query, id).Scan(
		&row.ID, &row.Category, &row.DisplayName, &row.Image, &imageDigest, &row.Binary, &versionCmd,
		&row.ArgSchemaJSON, &row.CommandTemplate, &capsJSON, &row.NetworkMode,
		&row.DefaultTimeoutS, &row.MaxOutputBytes, &row.DangerLevel, &requiresAppr,
		&enabled, &notes, &updatedAt,
	)
	if err != nil {
		return ToolDefRow{}, fmt.Errorf("read tool_defs %q: %w", id, err)
	}

	row.ImageDigest = imageDigest.String
	row.VersionCmd = versionCmd.String
	row.CapabilitiesJSON = capsJSON.String
	row.Notes = notes.String
	row.RequiresApproval = requiresAppr == 1
	row.Enabled = enabled == 1
	row.UpdatedAt = domain.TimestampMs(updatedAt)

	return row, nil
}

// List returns every definition in identifier order.
func (r *ToolDefsRepo) List(ctx context.Context) ([]ToolDefRow, error) {
	const query = `
		SELECT id FROM tool_defs ORDER BY id ASC`

	rows, err := r.db.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tool_defs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tool_defs id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool_defs: %w", err)
	}

	out := make([]ToolDefRow, 0, len(ids))
	for _, id := range ids {
		row, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// validateToolDef enforces the schema's CHECK constraints before a write.
func validateToolDef(row ToolDefRow) error {
	if strings.TrimSpace(row.ID) == "" {
		return fmt.Errorf("tool_defs id is empty: %w", ErrInvalidToolRun)
	}
	switch row.Category {
	case "recon", "osint", "web", "network", "exploit", "post", "report":
	default:
		return fmt.Errorf("tool_defs %q category %q: %w", row.ID, row.Category, ErrInvalidToolRun)
	}
	switch row.NetworkMode {
	case "bridge", "host", "none":
	default:
		return fmt.Errorf("tool_defs %q network_mode %q: %w", row.ID, row.NetworkMode, ErrInvalidToolRun)
	}
	switch row.DangerLevel {
	case "safe", "intrusive", "destructive":
	default:
		return fmt.Errorf("tool_defs %q danger_level %q: %w", row.ID, row.DangerLevel, ErrInvalidToolRun)
	}
	if row.DefaultTimeoutS <= 0 {
		return fmt.Errorf("tool_defs %q default_timeout_s %d: %w", row.ID, row.DefaultTimeoutS, ErrInvalidToolRun)
	}
	if !json.Valid([]byte(row.ArgSchemaJSON)) {
		return fmt.Errorf("tool_defs %q arg_schema_json is not JSON: %w", row.ID, ErrInvalidToolRun)
	}
	return nil
}

// boolToIntValue renders a bool as the schema's 0 or 1.
func boolToIntValue(value bool) int {
	if value {
		return 1
	}
	return 0
}
