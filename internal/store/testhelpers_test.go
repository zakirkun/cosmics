package store_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// baseTimestamp is a fixed instant so ordering assertions never depend on the
// wall clock.
const baseTimestamp domain.TimestampMs = 1_760_000_000_000

// openTestDB opens a fresh database under t.TempDir and registers its close.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "cosmics.db"))
	require.NoError(t, err, "opening a fresh database must succeed")
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// draftFixture builds a fully populated draft engagement plus one target. Every
// field differs from its zero value, so a round-trip test can catch a column
// written from the wrong struct field.
func draftFixture(
	t *testing.T,
	ids *domain.ULIDGenerator,
	name string,
	createdAt domain.TimestampMs,
) (domain.Engagement, []domain.Target) {
	t.Helper()

	target, err := domain.NewTarget(domain.TargetDomain, "portal.acme.test")
	require.NoError(t, err)

	engagement, err := domain.NewEngagement(domain.NewEngagementParams{
		Name:    name,
		Client:  "Acme Corporation",
		Mode:    domain.ModeFull,
		Profile: "reasoning-tier",
		Authorization: domain.Authorization{
			AuthorizedBy: "dana.reyes",
			Note:         "SOW-2026-114 signed engagement letter",
			AuthorizedAt: createdAt - 1_000,
		},
		Targets:      []domain.Target{target},
		BudgetMicros: 25_000_000,
		TokenBudget:  4_000_000,
		Now:          createdAt,
	})
	require.NoError(t, err)

	engagement.ID = ids.NewAt(createdAt)
	engagement.ConfigJSON = `{"tool_timeout_s":600,"models":["a","b"]}`
	engagement.SpentUSDMicros = 120_000
	engagement.TokensUsed = 9_500

	target.EngagementID = engagement.ID
	target.Raw = "  portal.acme.test  "
	target.Note = "primary external web surface"
	target.CreatedAt = createdAt

	return engagement, []domain.Target{target}
}

// auditFixture returns a valid entry ready to append. Target, action, and
// timestamp vary per call so the hashes differ.
func auditFixture(t *testing.T, action string, at domain.TimestampMs) store.AuditEntry {
	t.Helper()

	return store.AuditEntry{
		EngagementID: "01J00000000000000000000001",
		Actor:        string(domain.ActorAgent),
		Action:       action,
		Target:       "portal.acme.test",
		Command:      "nmap -sV -p 443 portal.acme.test",
		Outcome:      string(domain.OutcomeAllowed),
		DetailJSON:   `{"stage":"recon"}`,
		CreatedAt:    at,
	}
}

// readChain returns every audit row in insertion order, hashes included, so a
// test can assert the links directly.
func readChain(t *testing.T, db *store.DB) []store.AuditEntry {
	t.Helper()

	rows, err := db.Reader().QueryContext(t.Context(),
		`SELECT id, engagement_id, actor, action, target, command, outcome,
		        detail_json, prev_hash, entry_hash, created_at
		   FROM audit_trail ORDER BY rowid`)
	require.NoError(t, err)
	defer rows.Close()

	var entries []store.AuditEntry
	for rows.Next() {
		var (
			entry     store.AuditEntry
			createdAt int64
		)
		require.NoError(t, rows.Scan(
			&entry.ID, &entry.EngagementID, &entry.Actor, &entry.Action, &entry.Target,
			&entry.Command, &entry.Outcome, &entry.DetailJSON, &entry.PrevHash,
			&entry.EntryHash, &createdAt,
		))
		entry.CreatedAt = domain.TimestampMs(createdAt)
		entries = append(entries, entry)
	}
	require.NoError(t, rows.Err())
	return entries
}

// sqliteError is the driver's typed error, declared as an interface so a test
// can assert on the stable Code() method.
type sqliteError interface {
	error
	Code() int
}

// sqliteCodeOK is SQLITE_OK; a rejected statement must never report it.
const sqliteCodeOK = 0
