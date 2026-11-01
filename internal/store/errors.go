// Package store is the SQLite persistence layer.
//
// It owns the two-handle connection pool (one serialized writer, one read
// pool), the embedded migrations, and the repositories that map rows to
// domain types. It imports internal/domain and the standard library only.
package store

import (
	"errors"
	"fmt"
)

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("store: row not found")

	// ErrChecksumMismatch is returned when a migration file's bytes differ from
	// the checksum recorded when that version was applied. It means the binary
	// and the database disagree about the schema, which is never safe to ignore.
	ErrChecksumMismatch = errors.New("store: migration checksum mismatch")

	// ErrMigrationOrder is returned when the recorded migration history cannot
	// be reconciled with the embedded migration set: a version is recorded that
	// is not embedded, a version is missing between two applied versions, or two
	// files claim the same version.
	ErrMigrationOrder = errors.New("store: migration order is inconsistent")

	// ErrAuditChainBroken is the sentinel behind every audit verification
	// failure. Use errors.As with *AuditVerifyError to learn which row broke.
	ErrAuditChainBroken = errors.New("store: audit hash chain is broken")
)

// AuditVerifyError names the first row whose chain link or hash did not verify.
//
// It wraps ErrAuditChainBroken so a caller that only cares *whether* the trail
// is intact can write errors.Is(err, store.ErrAuditChainBroken), while a caller
// that wants to report the damage can use errors.As to read RowID.
type AuditVerifyError struct {
	// RowID is the primary key of the first row that failed verification.
	RowID string

	// Reason is a short, stable explanation of the failure.
	Reason string
}

// Error renders the row id and reason.
func (e *AuditVerifyError) Error() string {
	return fmt.Sprintf("audit row %s: %s", e.RowID, e.Reason)
}

// Unwrap exposes the sentinel so errors.Is works.
func (e *AuditVerifyError) Unwrap() error { return ErrAuditChainBroken }
