// Package memory implements the platform's durable memory: semantic facts with
// supersession, retrieval over the SQLite FTS5 indexes, and the deterministic
// engagement briefing described by docs/CORE.md section 6.3.
//
// It sits above internal/store: reads go through the reader pool, writes through
// the single writer handle, and every mutation that touches more than one row
// runs inside one transaction.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrNoActiveFact is returned when no unsuperseded fact matches a lookup.
	ErrNoActiveFact = errors.New("memory: no active fact")

	// ErrInvalidFact is returned when a fact would violate a schema CHECK or a
	// required-field rule. It is caught before SQL so the caller receives a
	// typed error naming the field.
	ErrInvalidFact = errors.New("memory: invalid fact")
)

// Fact value types, mirroring the facts.value_type CHECK constraint.
const (
	ValueTypeText   = "text"
	ValueTypeNumber = "number"
	ValueTypeBool   = "bool"
	ValueTypeJSON   = "json"
	ValueTypeRef    = "ref"
)

// Fact source kinds, mirroring the facts.source_kind CHECK constraint.
const (
	SourceToolRun      = "tool_run"
	SourceLLMInference = "llm_inference"
	SourceOperator     = "operator"
	SourceOSINT        = "osint"
)

// Fact is one durable statement about an engagement or one of its assets.
//
// SupersededBy is the zero ID while the row is the active version. History is
// append-plus-supersede: a row is never updated except to record that a newer
// version replaced it.
type Fact struct {
	ID           domain.ID
	EngagementID domain.ID
	Subject      string
	Predicate    string
	Value        string
	ValueType    string
	Confidence   float64
	SourceKind   string
	SourceID     string
	Stage        domain.StageKind
	SupersededBy domain.ID
	CreatedAt    domain.TimestampMs
}

// SetParams groups the inputs to FactRepo.Set. A struct keeps the three
// free-text key fields from being swapped positionally.
type SetParams struct {
	EngagementID domain.ID
	Subject      string
	Predicate    string
	Value        string
	ValueType    string
	Confidence   float64
	SourceKind   string
	SourceID     string
	Stage        domain.StageKind
	Now          domain.TimestampMs
}

// FactRepo stores and queries semantic memory.
type FactRepo struct {
	db  *store.DB
	ids *domain.ULIDGenerator
}

// NewFactRepo returns a repository over db. Fact identifiers are minted from a
// monotonic ULID generator so same-millisecond writes still sort in order.
func NewFactRepo(db *store.DB) *FactRepo {
	return &FactRepo{db: db, ids: domain.NewULIDGenerator()}
}

const factColumns = `id, engagement_id, subject, predicate, value, value_type,
	confidence, source_kind, source_id, stage, superseded_by, created_at`

const insertFactSQL = `INSERT INTO facts (` + factColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// supersedeSQL retires every other active version this write outranks.
//
// The comparison is `confidence <= ?`, so an equal-confidence write replaces the
// prior value and a higher-confidence write replaces a weaker one. A
// lower-confidence write matches no row, which is what leaves the stronger fact
// active (docs/ERD.md section 3.12). `id <> ?` excludes the row just inserted,
// which would otherwise supersede itself.
const supersedeSQL = `UPDATE facts SET superseded_by = ?
	WHERE engagement_id = ? AND subject = ? AND predicate = ?
	  AND superseded_by IS NULL AND confidence <= ? AND id <> ?`

// Set records a fact and applies the supersession rule in one transaction.
//
// An equal-or-higher-confidence write marks every other active version of the
// same (engagement, subject, predicate) as superseded by the new row. A
// lower-confidence write is still persisted — it is evidence the agent observed
// something — but it does not displace the stronger active fact.
//
// The INSERT runs before the UPDATE because superseded_by is a foreign key to
// facts(id): the target row must exist before a reference to it is legal.
func (r *FactRepo) Set(ctx context.Context, p SetParams) (Fact, error) {
	if err := p.validate(); err != nil {
		return Fact{}, err
	}

	fact := Fact{
		ID:           r.ids.NewAt(p.Now),
		EngagementID: p.EngagementID,
		Subject:      strings.TrimSpace(p.Subject),
		Predicate:    strings.TrimSpace(p.Predicate),
		Value:        p.Value,
		ValueType:    p.ValueType,
		Confidence:   p.Confidence,
		SourceKind:   p.SourceKind,
		SourceID:     p.SourceID,
		Stage:        p.Stage,
		CreatedAt:    p.Now,
	}

	// The identifier is minted once, outside the retried closure: a SQLITE_BUSY
	// retry must rewrite the same row, not mint a second one.
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertFactSQL,
			fact.ID.String(), fact.EngagementID.String(), fact.Subject,
			fact.Predicate, fact.Value, fact.ValueType, fact.Confidence,
			fact.SourceKind, nullText(fact.SourceID), string(fact.Stage),
			nil, int64(fact.CreatedAt),
		); err != nil {
			return fmt.Errorf("insert fact row: %w", err)
		}

		if _, err := tx.ExecContext(ctx, supersedeSQL,
			fact.ID.String(), fact.EngagementID.String(),
			fact.Subject, fact.Predicate, fact.Confidence, fact.ID.String(),
		); err != nil {
			return fmt.Errorf("supersede active facts: %w", err)
		}
		return nil
	})
	if err != nil {
		return Fact{}, fmt.Errorf("set fact %s[%s] for engagement %s: %w",
			fact.Subject, fact.Predicate, fact.EngagementID, err)
	}
	return fact, nil
}

// Active returns the authoritative unsuperseded fact for one key.
//
// Two rows can be active at once: a lower-confidence observation is persisted
// without displacing the stronger fact (ERD 3.12). Ordering by confidence first
// means the strongest assertion stays authoritative, and the newest row wins an
// equal-confidence tie.
func (r *FactRepo) Active(ctx context.Context, engagementID domain.ID, subject, predicate string) (Fact, error) {
	if engagementID.IsZero() {
		return Fact{}, fmt.Errorf("active fact %s[%s]: engagement id: %w", subject, predicate, ErrInvalidFact)
	}

	row := r.db.Reader().QueryRowContext(ctx,
		`SELECT `+factColumns+` FROM facts
		  WHERE engagement_id = ? AND subject = ? AND predicate = ? AND superseded_by IS NULL
		  ORDER BY confidence DESC, created_at DESC, id DESC LIMIT 1`,
		engagementID.String(), subject, predicate)

	fact, err := scanFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, fmt.Errorf("active fact %s[%s] in engagement %s: %w",
			subject, predicate, engagementID, ErrNoActiveFact)
	}
	if err != nil {
		return Fact{}, fmt.Errorf("active fact %s[%s] in engagement %s: %w",
			subject, predicate, engagementID, err)
	}
	return fact, nil
}

// ListActive returns every unsuperseded fact for an engagement, ordered by key
// so callers that render the list get a stable projection.
func (r *FactRepo) ListActive(ctx context.Context, engagementID domain.ID) ([]Fact, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list active facts: engagement id: %w", ErrInvalidFact)
	}
	return r.query(ctx,
		`SELECT `+factColumns+` FROM facts
		  WHERE engagement_id = ? AND superseded_by IS NULL
		  ORDER BY subject ASC, predicate ASC, created_at DESC, id DESC`,
		engagementID.String())
}

// History returns every version of one key, oldest first.
//
// Superseded rows are never deleted, so this is the full audit of what the
// engagement believed and when it changed its mind.
func (r *FactRepo) History(ctx context.Context, engagementID domain.ID, subject, predicate string) ([]Fact, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list fact history %s[%s]: engagement id: %w",
			subject, predicate, ErrInvalidFact)
	}
	return r.query(ctx,
		`SELECT `+factColumns+` FROM facts
		  WHERE engagement_id = ? AND subject = ? AND predicate = ?
		  ORDER BY created_at ASC, id ASC`,
		engagementID.String(), subject, predicate)
}

// query runs a fact projection and scans every row.
func (r *FactRepo) query(ctx context.Context, query string, args ...any) ([]Fact, error) {
	rows, err := r.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query facts: %w", err)
	}
	defer rows.Close()

	facts := make([]Fact, 0, 16)
	for rows.Next() {
		fact, err := scanFact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts: %w", err)
	}
	return facts, nil
}
