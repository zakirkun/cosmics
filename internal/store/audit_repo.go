package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// AuditEntry mirrors one audit_trail row.
type AuditEntry struct {
	ID           string
	EngagementID string
	Actor        string
	Action       string
	Target       string
	Command      string
	Outcome      string
	DetailJSON   string
	PrevHash     string
	EntryHash    string
	CreatedAt    domain.TimestampMs
}

// AuditRepo appends to and verifies the hash-chained audit trail.
type AuditRepo struct {
	db  *DB
	ids domain.IDGenerator
}

// NewAuditRepo returns a repository over db.
func NewAuditRepo(db *DB) *AuditRepo {
	return &AuditRepo{db: db, ids: domain.NewULIDGenerator()}
}

// auditColumns is the projection used by both the chain reader and Verify. The
// order matches scanAuditEntry.
const auditColumns = `id, engagement_id, actor, action, target, command, outcome,
	detail_json, prev_hash, entry_hash, created_at`

// canonicalAuditPayload renders the immutable fields of an entry into the exact
// byte string that is hashed.
//
// ENCODING (must not change without a new migration):
//
//	payload = enc(id) enc(engagement_id) enc(actor) enc(action) enc(target)
//	          enc(command) enc(outcome) enc(detail_json) enc(prev_hash) enc(created_at)
//	enc(v)  = decimal byte length of v, ":", v, "|"
//	created_at is encoded as its base-10 integer.
//
// Length prefixes make the encoding injective: no field value can shift bytes
// from one field into the next, so two different entries can never produce the
// same payload. NULL is read as the empty string, so a NULL and an empty value
// hash identically; rows are always written with "" for "absent" to keep the
// encoding total.
func canonicalAuditPayload(entry AuditEntry) string {
	var b strings.Builder
	b.Grow(256)
	for _, field := range []string{
		entry.ID,
		entry.EngagementID,
		entry.Actor,
		entry.Action,
		entry.Target,
		entry.Command,
		entry.Outcome,
		entry.DetailJSON,
		entry.PrevHash,
		strconv.FormatInt(int64(entry.CreatedAt), 10),
	} {
		b.WriteString(strconv.Itoa(len(field)))
		b.WriteByte(':')
		b.WriteString(field)
		b.WriteByte('|')
	}
	return b.String()
}

// computeAuditHash returns the lowercase hex SHA-256 of canonicalAuditPayload.
func computeAuditHash(entry AuditEntry) string {
	sum := sha256.Sum256([]byte(canonicalAuditPayload(entry)))
	return hex.EncodeToString(sum[:])
}

// Append writes one entry, linking it to the current chain head.
//
// The chain head is read inside the same transaction that inserts the row, so
// two appends can never link to the same predecessor. When entry.ID is empty a
// monotonic ULID is minted; callers should leave it empty so identifier order
// keeps matching insertion order. EntryHash and PrevHash are always computed
// here and never taken from the caller.
func (r *AuditRepo) Append(ctx context.Context, entry AuditEntry) error {
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := appendAuditTx(ctx, tx, r.ids, entry); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("append audit entry for %q: %w", entry.Action, err)
	}
	return nil
}

// appendAuditTx is the transaction-scoped append. Repositories that must record
// an audit entry atomically with their own write call this directly instead of
// opening a second transaction.
func appendAuditTx(ctx context.Context, tx *sql.Tx, ids domain.IDGenerator, entry AuditEntry) error {
	if strings.TrimSpace(entry.Action) == "" {
		return fmt.Errorf("audit action must not be empty")
	}
	if !domain.Actor(entry.Actor).Valid() {
		return fmt.Errorf("audit actor %q: %w", entry.Actor, domain.ErrUnknownValue)
	}
	if !domain.AuditOutcome(entry.Outcome).Valid() {
		return fmt.Errorf("audit outcome %q: %w", entry.Outcome, domain.ErrUnknownValue)
	}

	if entry.ID == "" {
		entry.ID = ids.New().String()
	}

	prevHash, err := auditHeadTx(ctx, tx)
	if err != nil {
		return err
	}
	entry.PrevHash = prevHash
	entry.EntryHash = computeAuditHash(entry)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_trail (`+auditColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.EngagementID, entry.Actor, entry.Action, entry.Target,
		entry.Command, entry.Outcome, entry.DetailJSON, entry.PrevHash,
		entry.EntryHash, int64(entry.CreatedAt),
	); err != nil {
		return fmt.Errorf("insert audit row: %w", err)
	}
	return nil
}

// auditHeadTx reads the entry_hash of the chain head inside tx, or "" when the
// trail is empty. rowid is insertion order; id is caller-controlled, so
// ordering by id would let a supplied identifier link the chain backwards.
func auditHeadTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var head string
	err := tx.QueryRowContext(ctx,
		`SELECT entry_hash FROM audit_trail ORDER BY rowid DESC LIMIT 1`).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read audit chain head: %w", err)
	}
	return head, nil
}

// LastHash returns the entry_hash of the newest entry, or "" when the trail is
// empty.
func (r *AuditRepo) LastHash(ctx context.Context) (string, error) {
	var head string
	err := r.db.Reader().QueryRowContext(ctx,
		`SELECT entry_hash FROM audit_trail ORDER BY rowid DESC LIMIT 1`).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read audit chain head: %w", err)
	}
	return head, nil
}

// Verify recomputes the whole chain in insertion order and returns the number
// of rows that verified.
//
// It checks both halves of the invariant for every row: that prev_hash equals
// the previous row's entry_hash, and that entry_hash equals the recomputed
// hash. The first failure stops the walk and returns an *AuditVerifyError
// wrapping ErrAuditChainBroken, so errors.Is(err, ErrAuditChainBroken) answers
// "is the trail intact" and errors.As reveals the damaged row.
func (r *AuditRepo) Verify(ctx context.Context) (int, error) {
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT `+auditColumns+` FROM audit_trail ORDER BY rowid`)
	if err != nil {
		return 0, fmt.Errorf("read audit trail: %w", err)
	}
	defer rows.Close()

	verified := 0
	prevHash := ""

	for rows.Next() {
		entry, err := scanAuditEntry(rows)
		if err != nil {
			return verified, fmt.Errorf("scan audit row: %w", err)
		}

		if entry.PrevHash != prevHash {
			return verified, &AuditVerifyError{
				RowID: entry.ID,
				Reason: fmt.Sprintf("prev_hash %q does not match the previous entry_hash %q",
					entry.PrevHash, prevHash),
			}
		}

		expected := computeAuditHash(entry)
		if entry.EntryHash != expected {
			return verified, &AuditVerifyError{
				RowID:  entry.ID,
				Reason: fmt.Sprintf("entry_hash %q does not match recomputed hash %q", entry.EntryHash, expected),
			}
		}

		prevHash = entry.EntryHash
		verified++
	}

	if err := rows.Err(); err != nil {
		return verified, fmt.Errorf("iterate audit trail: %w", err)
	}
	return verified, nil
}

// scanAuditEntry maps one auditColumns row, folding NULL into the empty string
// so the hash encoding stays total.
func scanAuditEntry(scanner rowScanner) (AuditEntry, error) {
	var (
		entry                                     AuditEntry
		engagementID, target, command, detailJSON sql.NullString
		prevHash                                  sql.NullString
		createdAt                                 int64
	)
	err := scanner.Scan(
		&entry.ID, &engagementID, &entry.Actor, &entry.Action, &target,
		&command, &entry.Outcome, &detailJSON, &prevHash, &entry.EntryHash, &createdAt,
	)
	if err != nil {
		return AuditEntry{}, err
	}
	entry.EngagementID = engagementID.String
	entry.Target = target.String
	entry.Command = command.String
	entry.DetailJSON = detailJSON.String
	entry.PrevHash = prevHash.String
	entry.CreatedAt = domain.TimestampMs(createdAt)
	return entry, nil
}

// ListByEngagement returns the audit rows for one engagement, newest first.
func (r *AuditRepo) ListByEngagement(ctx context.Context, engagementID domain.ID) ([]AuditEntry, error) {
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT `+auditColumns+` FROM audit_trail WHERE engagement_id = ? ORDER BY id DESC`,
		engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list audit entries for engagement %q: %w", engagementID, err)
	}
	defer rows.Close()

	entries := make([]AuditEntry, 0, 8)
	for rows.Next() {
		entry, err := scanAuditEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}
