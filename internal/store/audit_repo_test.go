package store_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_AuditAppend_builds_a_valid_chain_when_entries_are_added(t *testing.T) {
	// Given: an empty audit trail and three distinct entries.
	db := openTestDB(t)
	repo := store.NewAuditRepo(db)

	inputs := []store.AuditEntry{
		auditFixture(t, "engagement_created", baseTimestamp),
		auditFixture(t, "tool_executed", baseTimestamp+1_000),
		auditFixture(t, "report_generated", baseTimestamp+2_000),
	}

	// When: all three are appended.
	for _, entry := range inputs {
		require.NoError(t, repo.Append(t.Context(), entry))
	}

	// Then: Verify walks the whole chain and reports three good rows.
	verified, err := repo.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 3, verified)

	// Then: each row links to its predecessor, and the first links to nothing.
	chain := readChain(t, db)
	require.Len(t, chain, 3)
	assert.Empty(t, chain[0].PrevHash, "the first entry starts the chain")
	for i := 1; i < len(chain); i++ {
		assert.Equal(t, chain[i-1].EntryHash, chain[i].PrevHash,
			"row %d must link to row %d", i, i-1)
	}

	// Then: every hash is a distinct 64-character lowercase hex digest.
	seen := make(map[string]struct{}, len(chain))
	for _, entry := range chain {
		assert.Len(t, entry.EntryHash, 64)
		assert.Regexp(t, "^[0-9a-f]{64}$", entry.EntryHash)
		_, duplicate := seen[entry.EntryHash]
		assert.False(t, duplicate, "hashes must not repeat")
		seen[entry.EntryHash] = struct{}{}
	}

	// Then: LastHash agrees with the final link.
	last, err := repo.LastHash(t.Context())
	require.NoError(t, err)
	assert.Equal(t, chain[len(chain)-1].EntryHash, last)
}

func Test_AuditAppend_links_to_an_empty_prev_hash_when_it_is_the_first_entry(t *testing.T) {
	// Given: an empty audit trail.
	db := openTestDB(t)
	repo := store.NewAuditRepo(db)

	// When: one entry is appended.
	require.NoError(t, repo.Append(t.Context(), auditFixture(t, "engagement_created", baseTimestamp)))

	// Then: its prev_hash is empty and it is verifiable, and LastHash is empty
	// before the first append.
	chain := readChain(t, db)
	require.Len(t, chain, 1)
	assert.Empty(t, chain[0].PrevHash)
	assert.NotEmpty(t, chain[0].EntryHash)

	verified, err := repo.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, verified)
}

func Test_AuditLastHash_is_empty_when_the_trail_is_empty(t *testing.T) {
	// Given: an empty audit trail.
	db := openTestDB(t)
	repo := store.NewAuditRepo(db)

	// When: the chain head is requested.
	last, err := repo.LastHash(t.Context())

	// Then: there is no head but that is not an error.
	require.NoError(t, err)
	assert.Empty(t, last)

	// Then: Verify finds nothing to check and says so without failing.
	verified, err := repo.Verify(t.Context())
	require.NoError(t, err)
	assert.Zero(t, verified)
}

func Test_AuditVerify_detects_tampering_when_a_row_is_rewritten(t *testing.T) {
	// Given: a separate throwaway database with three appended entries.
	//
	// The append-only triggers are DROPPED here on purpose. They are the first
	// line of defence and they work (see the trigger test in migrate_test.go),
	// but Verify exists for the case the triggers cannot cover: someone with the
	// file and a SQLite shell, or a restore from a modified backup. Dropping the
	// trigger in a private TempDir database is the only way to produce that
	// situation, and without this test Verify could be a no-op and nothing would
	// notice.
	path := filepath.Join(t.TempDir(), "tampered.db")
	db, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	repo := store.NewAuditRepo(db)
	for _, at := range []domain.TimestampMs{baseTimestamp, baseTimestamp + 1_000, baseTimestamp + 2_000} {
		require.NoError(t, repo.Append(t.Context(), auditFixture(t, "tool_executed", at)))
	}
	require.NoError(t, func() error {
		_, execErr := repo.Verify(t.Context())
		return execErr
	}())

	chain := readChain(t, db)
	require.Len(t, chain, 3)
	tamperedID := chain[1].ID

	_, err = db.Writer().ExecContext(t.Context(),
		`DROP TRIGGER audit_no_update; DROP TRIGGER audit_no_delete;`)
	require.NoError(t, err)

	// When: the middle row's action is rewritten in place, keeping its old hash.
	_, err = db.Writer().ExecContext(t.Context(),
		`UPDATE audit_trail SET action = 'config_changed' WHERE id = ?`, tamperedID)
	require.NoError(t, err, "the trigger is gone, so the UPDATE must be allowed")

	// Then: Verify stops at that row and names it.
	verified, err := repo.Verify(t.Context())

	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrAuditChainBroken)
	assert.Equal(t, 1, verified, "the rows before the tampered one still verify")

	var verifyErr *store.AuditVerifyError
	require.ErrorAs(t, err, &verifyErr)
	assert.Equal(t, tamperedID, verifyErr.RowID, "the error must name the damaged row")
	assert.Contains(t, verifyErr.Reason, "entry_hash", "a rewritten payload breaks the hash, not the link")
}

func Test_AuditVerify_detects_a_broken_link_when_a_row_is_deleted(t *testing.T) {
	// Given: a throwaway database with four entries whose triggers are dropped.
	path := filepath.Join(t.TempDir(), "truncated.db")
	db, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	repo := store.NewAuditRepo(db)
	for i := range 4 {
		require.NoError(t, repo.Append(t.Context(),
			auditFixture(t, "tool_executed", baseTimestamp+domain.TimestampMs(i)*1_000)))
	}

	chain := readChain(t, db)
	require.Len(t, chain, 4)
	removedID := chain[1].ID

	_, err = db.Writer().ExecContext(t.Context(), `DROP TRIGGER audit_no_delete`)
	require.NoError(t, err)

	// When: the second row is removed, orphaning the third row's prev_hash.
	_, err = db.Writer().ExecContext(t.Context(),
		`DELETE FROM audit_trail WHERE id = ?`, removedID)
	require.NoError(t, err, "the trigger is gone, so the DELETE must be allowed")

	// Then: the chain link check catches it at the first orphaned row.
	verified, err := repo.Verify(t.Context())

	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrAuditChainBroken)
	assert.Equal(t, 1, verified)

	var verifyErr *store.AuditVerifyError
	require.ErrorAs(t, err, &verifyErr)
	assert.Equal(t, chain[2].ID, verifyErr.RowID)
	assert.Contains(t, verifyErr.Reason, "prev_hash", "a deletion breaks the link, not the hash")
}

func Test_AuditAppend_rejects_an_invalid_actor_when_the_domain_does_not_know_it(t *testing.T) {
	// Given: an entry whose actor is not one of operator/agent/system.
	db := openTestDB(t)
	repo := store.NewAuditRepo(db)
	entry := auditFixture(t, "tool_executed", baseTimestamp)
	entry.Actor = "intern"

	// When: it is appended.
	err := repo.Append(t.Context(), entry)

	// Then: it is rejected before reaching SQL and nothing was written.
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnknownValue)

	entries := readChain(t, db)
	assert.Empty(t, entries)
}

func Test_AuditAppend_rejects_an_empty_action(t *testing.T) {
	// Given: an entry with no action.
	db := openTestDB(t)
	repo := store.NewAuditRepo(db)
	entry := auditFixture(t, "", baseTimestamp)

	// When: it is appended.
	err := repo.Append(t.Context(), entry)

	// Then: it is rejected and the trail stays empty.
	require.Error(t, err)
	assert.Empty(t, readChain(t, db))
}
