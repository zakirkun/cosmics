package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// EngagementRepo reads and writes the engagements aggregate.
type EngagementRepo struct {
	db  *DB
	ids domain.IDGenerator
}

// NewEngagementRepo returns a repository over db. Identifiers for rows that the
// caller did not pre-assign (targets and audit entries) are minted from a
// monotonic ULID generator.
func NewEngagementRepo(db *DB) *EngagementRepo {
	return &EngagementRepo{db: db, ids: domain.NewULIDGenerator()}
}

const insertEngagementSQL = `INSERT INTO engagements (
	id, name, client, mode, status, stage, model_profile,
	authorized_by, authorization_note, authorized_at,
	budget_usd_micros, spent_usd_micros, token_budget, tokens_used,
	created_at, updated_at, started_at, finished_at, config_json, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const insertTargetSQL = `INSERT INTO engagement_targets (
	id, engagement_id, kind, value, raw, note, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

// Create persists an engagement with its seed targets and the two creation-time
// audit entries, all in one transaction.
//
// The caller supplies the engagement ID because Create cannot return it. Target
// IDs are taken from the values when set and minted otherwise; the target's
// EngagementID field is always overwritten with the aggregate's ID, which is the
// only correct value. The authorization values are written verbatim and are
// immutable afterwards, enforced by a trigger.
func (r *EngagementRepo) Create(ctx context.Context, engagement domain.Engagement, targets []domain.Target) error {
	if engagement.ID.IsZero() {
		return fmt.Errorf("create engagement %q: %w", engagement.Name, domain.ErrEmptyID)
	}
	if err := engagement.Authorization.Validate(); err != nil {
		return fmt.Errorf("create engagement %q: %w", engagement.Name, err)
	}
	if len(targets) == 0 {
		return fmt.Errorf("create engagement %q: %w", engagement.Name, domain.ErrNoTargets)
	}

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertEngagementSQL,
			engagement.ID.String(), engagement.Name, nullString(engagement.Client),
			string(engagement.Mode), string(engagement.Status), string(engagement.Stage),
			engagement.Profile,
			engagement.Authorization.AuthorizedBy, nullString(engagement.Authorization.Note),
			int64(engagement.Authorization.AuthorizedAt),
			engagement.BudgetUSDMicros, engagement.SpentUSDMicros,
			engagement.TokenBudget, engagement.TokensUsed,
			int64(engagement.CreatedAt), int64(engagement.UpdatedAt),
			nullTimestamp(engagement.StartedAt), nullTimestamp(engagement.FinishedAt),
			engagement.ConfigJSON, nullString(engagement.Err),
		); err != nil {
			return fmt.Errorf("insert engagement row: %w", err)
		}

		for i, target := range targets {
			id := target.ID
			if id.IsZero() {
				id = r.ids.New()
			}
			createdAt := target.CreatedAt
			if createdAt.IsZero() {
				createdAt = engagement.CreatedAt
			}
			if _, err := tx.ExecContext(ctx, insertTargetSQL,
				id.String(), engagement.ID.String(), string(target.Kind), target.Value,
				nullString(target.Raw), nullString(target.Note), int64(createdAt),
			); err != nil {
				return fmt.Errorf("insert target %d (%s %q): %w", i, target.Kind, target.Value, err)
			}
		}

		createdDetail, err := json.Marshal(map[string]any{
			"name":    engagement.Name,
			"client":  engagement.Client,
			"mode":    string(engagement.Mode),
			"targets": len(targets),
		})
		if err != nil {
			return fmt.Errorf("encode engagement_created detail: %w", err)
		}
		if err := appendAuditTx(ctx, tx, r.ids, AuditEntry{
			EngagementID: engagement.ID.String(),
			Actor:        string(domain.ActorOperator),
			Action:       string(domain.AuditEngagementCreated),
			Target:       engagement.Name,
			Outcome:      string(domain.OutcomeAllowed),
			DetailJSON:   string(createdDetail),
			CreatedAt:    engagement.CreatedAt,
		}); err != nil {
			return fmt.Errorf("append engagement_created audit entry: %w", err)
		}

		authDetail, err := json.Marshal(map[string]any{
			"authorized_by": engagement.Authorization.AuthorizedBy,
			"authorized_at": int64(engagement.Authorization.AuthorizedAt),
			"note":          engagement.Authorization.Note,
		})
		if err != nil {
			return fmt.Errorf("encode authorization_recorded detail: %w", err)
		}
		if err := appendAuditTx(ctx, tx, r.ids, AuditEntry{
			EngagementID: engagement.ID.String(),
			Actor:        string(domain.ActorOperator),
			Action:       string(domain.AuditAuthorizationRecorded),
			Target:       engagement.Authorization.AuthorizedBy,
			Outcome:      string(domain.OutcomeAllowed),
			DetailJSON:   string(authDetail),
			CreatedAt:    engagement.Authorization.AuthorizedAt,
		}); err != nil {
			return fmt.Errorf("append authorization_recorded audit entry: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("create engagement %q: %w", engagement.Name, err)
	}
	return nil
}

// Get loads one engagement by ID, returning ErrNotFound when it is absent.
func (r *EngagementRepo) Get(ctx context.Context, id domain.ID) (domain.Engagement, error) {
	row := r.db.Reader().QueryRowContext(ctx,
		`SELECT `+engagementColumns+` FROM engagements WHERE id = ?`, id.String())

	engagement, err := scanEngagement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Engagement{}, fmt.Errorf("get engagement %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("get engagement %q: %w", id, err)
	}
	return engagement, nil
}

// GetByName loads one engagement by its unique name.
func (r *EngagementRepo) GetByName(ctx context.Context, name string) (domain.Engagement, error) {
	row := r.db.Reader().QueryRowContext(ctx,
		`SELECT `+engagementColumns+` FROM engagements WHERE name = ?`, name)

	engagement, err := scanEngagement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Engagement{}, fmt.Errorf("get engagement by name %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("get engagement by name %q: %w", name, err)
	}
	return engagement, nil
}

// List returns every engagement, newest first.
func (r *EngagementRepo) List(ctx context.Context) ([]domain.Engagement, error) {
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT `+engagementColumns+` FROM engagements ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list engagements: %w", err)
	}
	defer rows.Close()

	engagements := make([]domain.Engagement, 0, 16)
	for rows.Next() {
		engagement, err := scanEngagement(rows)
		if err != nil {
			return nil, fmt.Errorf("scan engagement: %w", err)
		}
		engagements = append(engagements, engagement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate engagements: %w", err)
	}
	return engagements, nil
}

// UpdateStatus applies a compare-and-set status move and appends the
// engagement_status_changed audit entry in the same transaction.
//
// The UPDATE is guarded by `WHERE id = ? AND status = ?`, so two concurrent
// callers holding the same `from` value cannot both win: the loser affects zero
// rows and receives domain.ErrIllegalMove. A missing engagement yields
// ErrNotFound instead. The legality of the move itself is checked against the
// domain transition matrix before touching the database.
func (r *EngagementRepo) UpdateStatus(
	ctx context.Context,
	id domain.ID,
	from, to domain.Status,
	stage domain.EngageStage,
	at domain.TimestampMs,
) error {
	if err := from.Transition(to); err != nil {
		return fmt.Errorf("update engagement %q status: %w", id, err)
	}
	if !stage.Valid() {
		return fmt.Errorf("update engagement %q status: stage %q: %w", id, stage, domain.ErrUnknownStage)
	}

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE engagements SET status = ?, stage = ?, updated_at = ? WHERE id = ? AND status = ?`,
			string(to), string(stage), int64(at), id.String(), string(from))
		if err != nil {
			return fmt.Errorf("update engagement row: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count updated engagement rows: %w", err)
		}
		if affected == 0 {
			return r.classifyMissingMove(ctx, tx, id, from, to)
		}

		detail, err := json.Marshal(map[string]any{
			"from":  string(from),
			"to":    string(to),
			"stage": string(stage),
		})
		if err != nil {
			return fmt.Errorf("encode status change detail: %w", err)
		}
		return appendAuditTx(ctx, tx, r.ids, AuditEntry{
			EngagementID: id.String(),
			Actor:        string(domain.ActorSystem),
			Action:       string(domain.AuditEngagementStatusChanged),
			Target:       id.String(),
			Outcome:      string(domain.OutcomeAllowed),
			DetailJSON:   string(detail),
			CreatedAt:    at,
		})
	})
	if err != nil {
		return fmt.Errorf("update engagement %q status %s -> %s: %w", id, from, to, err)
	}
	return nil
}

// classifyMissingMove turns "zero rows updated" into the right typed error: the
// row is gone (ErrNotFound) or it is no longer in the `from` status, which is
// exactly the lost race (domain.ErrIllegalMove).
func (r *EngagementRepo) classifyMissingMove(
	ctx context.Context,
	tx *sql.Tx,
	id domain.ID,
	from, to domain.Status,
) error {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT status FROM engagements WHERE id = ?`, id.String()).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("engagement %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read current engagement status: %w", err)
	}
	return fmt.Errorf("engagement %q is %s, not %s (%s -> %s): %w",
		id, current, from, from, to, domain.ErrIllegalMove)
}

// nullString stores SQL NULL for the empty string. Optional text columns are
// nullable in the schema; an empty value is not the same thing as "unset", but
// no consumer distinguishes them.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullTimestamp stores SQL NULL for an unset timestamp.
func nullTimestamp(ts domain.TimestampMs) any {
	if ts.IsZero() {
		return nil
	}
	return int64(ts)
}
