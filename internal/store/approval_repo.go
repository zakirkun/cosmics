package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors for the approvals table.
var (
	// ErrInvalidApproval is returned when an approval row violates a column
	// contract before it reaches SQLite.
	ErrInvalidApproval = errors.New("store: invalid approval")

	// ErrApprovalAlreadyDecided is returned when a caller tries to move a row
	// that has already left 'pending'. The database trigger enforces the same
	// rule; this error lets a caller distinguish "too late" from "not found".
	ErrApprovalAlreadyDecided = errors.New("store: approval is already decided")

	// ErrApprovalExpired is returned when a caller tries to decide a pending
	// row whose deadline has passed. The row is flipped to 'expired' as part of
	// the same transaction, so a retry sees a decided row.
	ErrApprovalExpired = errors.New("store: approval deadline has passed")
)

// approvalKinds mirrors the approvals.kind CHECK list.
var approvalKinds = []string{
	"destructive_action", "exploit_launch", "credential_use", "data_access",
}

// approvalDecisions mirrors the approvals.decision CHECK list.
var approvalDecisions = []string{"pending", "approved", "denied", "expired"}

// The individual decision values, so the SQL comparisons cannot drift from the
// CHECK list above.
const (
	decisionPending = "pending"
	decisionExpired = "expired"
)

// parseOptionalID converts a nullable id column, tolerating NULL and any value
// the domain rejects by collapsing it to the zero identifier: an unreadable
// foreign key must not make the whole row unreadable.
func parseOptionalID(raw sql.NullString) domain.ID {
	if !raw.Valid || raw.String == "" {
		return domain.ID{}
	}
	id, err := domain.NewID(raw.String)
	if err != nil {
		return domain.ID{}
	}
	return id
}

// IsApprovalKind reports whether kind satisfies the approvals CHECK.
func IsApprovalKind(kind string) bool { return slices.Contains(approvalKinds, kind) }

// IsApprovalDecision reports whether decision satisfies the approvals CHECK.
func IsApprovalDecision(decision string) bool {
	return slices.Contains(approvalDecisions, decision)
}

// ApprovalRow mirrors one approvals row.
//
// It is a plain projection rather than a domain type so this package keeps its
// single dependency (internal/domain) and the higher-level approval package can
// wrap it without an import cycle.
type ApprovalRow struct {
	ID             domain.ID
	EngagementID   domain.ID
	ToolRunID      domain.ID
	LLMCallID      domain.ID
	Kind           string
	CommandPreview string
	Reason         string
	Decision       string
	DecidedAt      domain.TimestampMs
	DecidedBy      string
	ExpiresAt      domain.TimestampMs
	CreatedAt      domain.TimestampMs
}

// ApprovalRepo reads and writes the approvals table.
type ApprovalRepo struct{ db *DB }

// NewApprovalRepo returns a repository over the shared database handle.
func NewApprovalRepo(db *DB) *ApprovalRepo { return &ApprovalRepo{db: db} }

const approvalColumns = `id, engagement_id, tool_run_id, requested_by_llm_call_id,
	kind, command_preview, reason, decision, decided_at, decided_by,
	expires_at, created_at`

// Create inserts a new approval row.
//
// A new row always starts pending: callers persist the request before the
// operator has answered, so accepting a decision here would let an agent mint
// its own approval. Decide is the only path to a decision.
func (r *ApprovalRepo) Create(ctx context.Context, row ApprovalRow) (ApprovalRow, error) {
	if err := validateApprovalRow(row); err != nil {
		return ApprovalRow{}, err
	}

	row.Decision = string(decisionPending)
	row.DecidedAt = 0
	row.DecidedBy = ""

	const query = `INSERT INTO approvals (` + approvalColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?)
		RETURNING ` + approvalColumns

	stored, err := scanApproval(r.db.Writer().QueryRowContext(ctx, query,
		row.ID.String(), row.EngagementID.String(),
		nullID(row.ToolRunID), nullID(row.LLMCallID),
		row.Kind, row.CommandPreview, row.Reason, row.Decision,
		int64(row.ExpiresAt), int64(row.CreatedAt),
	))
	if err != nil {
		return ApprovalRow{}, fmt.Errorf("create approval %s: %w", row.ID, err)
	}
	return stored, nil
}

// Get returns one approval row by id.
func (r *ApprovalRepo) Get(ctx context.Context, id domain.ID) (ApprovalRow, error) {
	if id.IsZero() {
		return ApprovalRow{}, fmt.Errorf("get approval: %w", domain.ErrEmptyID)
	}
	row, err := scanApproval(r.db.Reader().QueryRowContext(ctx,
		`SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id.String()))
	if err != nil {
		return ApprovalRow{}, fmt.Errorf("get approval %s: %w", id, err)
	}
	return row, nil
}

// Decide records the operator's answer for a pending row.
//
// Three outcomes are possible and all three are explicit:
//   - the row is pending and in time: it moves to decision, with decided_at and
//     decided_by set, and is returned;
//   - the row already left pending: ErrApprovalAlreadyDecided, nothing written;
//   - the row is pending but past expires_at: it is flipped to expired (nobody
//     may approve a lapsed request) and ErrApprovalExpired is returned.
//
// The UPDATE re-asserts decision='pending' so two racing callers cannot both
// win; the loser is told the row was already decided.
func (r *ApprovalRepo) Decide(ctx context.Context, id domain.ID, decision string, decidedBy string, at domain.TimestampMs) (ApprovalRow, error) {
	if id.IsZero() {
		return ApprovalRow{}, fmt.Errorf("decide approval: %w", domain.ErrEmptyID)
	}
	if !IsApprovalDecision(decision) || decision == string(decisionPending) {
		return ApprovalRow{}, fmt.Errorf("decide approval %s: decision %q: %w", id, decision, ErrInvalidApproval)
	}
	if at.IsZero() {
		return ApprovalRow{}, fmt.Errorf("decide approval %s: decided_at: %w", id, ErrInvalidApproval)
	}

	var stored ApprovalRow
	var lapsedAt domain.TimestampMs

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var currentDecision string
		var expiresAt int64
		err := tx.QueryRowContext(ctx,
			`SELECT decision, expires_at FROM approvals WHERE id = ?`, id.String()).
			Scan(&currentDecision, &expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read approval state: %w", err)
		}
		if currentDecision != decisionPending {
			return fmt.Errorf("approval %s is %q: %w", id, currentDecision, ErrApprovalAlreadyDecided)
		}

		effective := decision
		lapsed := int64(at) >= expiresAt
		if lapsed {
			effective = decisionExpired
			lapsedAt = domain.TimestampMs(expiresAt)
		}

		result, execErr := tx.ExecContext(ctx,
			`UPDATE approvals SET decision = ?, decided_at = ?, decided_by = ?
			   WHERE id = ? AND decision = ?`,
			effective, int64(at), nullString(decidedBy), id.String(), decisionPending)
		if execErr != nil {
			return fmt.Errorf("update approval decision: %w", execErr)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("update approval decision: %w", rowsErr)
		}
		if affected == 0 {
			return fmt.Errorf("approval %s changed underneath the decision: %w", id, ErrApprovalAlreadyDecided)
		}

		stored, err = scanApproval(tx.QueryRowContext(ctx,
			`SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id.String()))
		return err
	})
	if err != nil {
		return ApprovalRow{}, fmt.Errorf("decide approval %s: %w", id, err)
	}

	// Reported after the commit: a lapsed request must be recorded as expired,
	// so failing here inside the transaction would roll the flip back.
	if !lapsedAt.IsZero() {
		return stored, fmt.Errorf("approval %s deadline %d: %w", id, lapsedAt, ErrApprovalExpired)
	}
	return stored, nil
}

// ListPending returns the pending rows for an engagement, soonest deadline
// first so the approval queue is ordered by urgency.
func (r *ApprovalRepo) ListPending(ctx context.Context, engagementID domain.ID) ([]ApprovalRow, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list pending approvals: %w", domain.ErrEmptyID)
	}
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT `+approvalColumns+` FROM approvals
		  WHERE engagement_id = ? AND decision = ?
		  ORDER BY expires_at ASC, created_at ASC`,
		engagementID.String(), string(decisionPending))
	if err != nil {
		return nil, fmt.Errorf("list pending approvals for %s: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()

	var pending []ApprovalRow
	for rows.Next() {
		row, scanErr := scanApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending approvals for %s: %w", engagementID, err)
	}
	return pending, nil
}

// ExpirePending flips every overdue pending row to expired and reports how many
// it closed. Scoping is optional: a zero engagement id sweeps every engagement,
// which is what startup recovery wants.
func (r *ApprovalRepo) ExpirePending(ctx context.Context, engagementID domain.ID, now domain.TimestampMs) (int, error) {
	if now.IsZero() {
		return 0, fmt.Errorf("expire pending approvals: now: %w", ErrInvalidApproval)
	}

	var (
		query string
		args  []any
	)
	if engagementID.IsZero() {
		query = `UPDATE approvals SET decision = ?, decided_at = ?
			WHERE decision = ? AND expires_at <= ?`
		args = []any{decisionExpired, int64(now), decisionPending, int64(now)}
	} else {
		query = `UPDATE approvals SET decision = ?, decided_at = ?
			WHERE engagement_id = ? AND decision = ? AND expires_at <= ?`
		args = []any{decisionExpired, int64(now), engagementID.String(), decisionPending, int64(now)}
	}

	result, err := r.db.Writer().ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("expire pending approvals: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expire pending approvals: %w", err)
	}
	return int(affected), nil
}

// validateApprovalRow rejects values that the CHECK constraints would reject,
// naming the offending field so the failure is actionable before SQL runs.
func validateApprovalRow(row ApprovalRow) error {
	if row.ID.IsZero() {
		return fmt.Errorf("id: %w", domain.ErrEmptyID)
	}
	if row.EngagementID.IsZero() {
		return fmt.Errorf("engagement_id: %w", domain.ErrEmptyID)
	}
	if !IsApprovalKind(row.Kind) {
		return fmt.Errorf("kind %q: %w", row.Kind, ErrInvalidApproval)
	}
	if row.CommandPreview == "" {
		return fmt.Errorf("command_preview: %w", ErrInvalidApproval)
	}
	if row.Reason == "" {
		return fmt.Errorf("reason: %w", ErrInvalidApproval)
	}
	if row.CreatedAt.IsZero() {
		return fmt.Errorf("created_at: %w", ErrInvalidApproval)
	}
	if row.ExpiresAt <= row.CreatedAt {
		return fmt.Errorf("expires_at %d must be after created_at %d: %w",
			row.ExpiresAt, row.CreatedAt, ErrInvalidApproval)
	}
	return nil
}

// scanApproval maps one approvalColumns row.
func scanApproval(scanner rowScanner) (ApprovalRow, error) {
	var (
		row       ApprovalRow
		id, engID string
		toolRunID sql.NullString
		llmCallID sql.NullString
		decidedAt sql.NullInt64
		decidedBy sql.NullString
		expiresAt int64
		createdAt int64
	)

	if err := scanner.Scan(
		&id, &engID, &toolRunID, &llmCallID,
		&row.Kind, &row.CommandPreview, &row.Reason, &row.Decision,
		&decidedAt, &decidedBy, &expiresAt, &createdAt,
	); err != nil {
		if isNoRows(err) {
			return ApprovalRow{}, ErrNotFound
		}
		return ApprovalRow{}, fmt.Errorf("scan approval: %w", err)
	}

	parsedID, err := domain.NewID(id)
	if err != nil {
		return ApprovalRow{}, fmt.Errorf("scan approval id: %w", err)
	}
	parsedEng, err := domain.NewID(engID)
	if err != nil {
		return ApprovalRow{}, fmt.Errorf("scan approval engagement id: %w", err)
	}

	row.ID = parsedID
	row.EngagementID = parsedEng
	row.ToolRunID = parseOptionalID(toolRunID)
	row.LLMCallID = parseOptionalID(llmCallID)
	row.DecidedAt = domain.TimestampMs(decidedAt.Int64)
	row.DecidedBy = decidedBy.String
	row.ExpiresAt = domain.TimestampMs(expiresAt)
	row.CreatedAt = domain.TimestampMs(createdAt)
	return row, nil
}
