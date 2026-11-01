package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/store"
)

func Test_Open_applies_wal_and_foreign_keys_when_the_database_is_fresh(t *testing.T) {
	// Given: a database created from scratch by Open.
	db := openTestDB(t)

	// When: the connection pragmas are read back from each handle.
	var journalMode string
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journalMode))

	var foreignKeys int
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys))

	var writerForeignKeys int
	require.NoError(t, db.Writer().
		QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&writerForeignKeys))

	var busyTimeout int
	require.NoError(t, db.Writer().
		QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout))

	// Then: WAL is persistent on the file and FK enforcement is on per
	// connection, on both handles.
	assert.Equal(t, "wal", journalMode)
	assert.Equal(t, 1, foreignKeys)
	assert.Equal(t, 1, writerForeignKeys)
	assert.Equal(t, 5000, busyTimeout)
}

func Test_Healthy_succeeds_when_both_handles_answer(t *testing.T) {
	// Given: an open database.
	db := openTestDB(t)

	// When: the health check runs.
	err := db.Healthy(t.Context())

	// Then: it reports no problem.
	require.NoError(t, err)
}

func Test_Tx_rolls_back_when_the_function_returns_an_error(t *testing.T) {
	// Given: an open database with an empty audit trail and a sentinel failure.
	db := openTestDB(t)
	sentinel := errors.New("deliberate failure")

	// When: a transaction inserts a row and then fails.
	err := db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(t.Context(),
			`INSERT INTO audit_trail
			 (id, engagement_id, actor, action, outcome, prev_hash, entry_hash, created_at)
			 VALUES ('A1', '', 'system', 'engagement_created', 'allowed', '', 'h', 1)`)
		if execErr != nil {
			return execErr
		}
		return sentinel
	})

	// Then: the caller sees its own error and nothing was committed.
	require.ErrorIs(t, err, sentinel)

	var rows int
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_trail`).Scan(&rows))
	assert.Zero(t, rows, "a rolled-back transaction must leave no rows")
}

func Test_Tx_commits_when_the_function_returns_nil(t *testing.T) {
	// Given: an open database.
	db := openTestDB(t)

	// When: a transaction inserts a row and succeeds.
	err := db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(t.Context(),
			`INSERT INTO audit_trail
			 (id, engagement_id, actor, action, outcome, prev_hash, entry_hash, created_at)
			 VALUES ('A1', '', 'system', 'engagement_created', 'allowed', '', 'h', 1)`)
		return execErr
	})
	require.NoError(t, err)

	// Then: the row is visible through the reader handle.
	var rows int
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_trail`).Scan(&rows))
	assert.Equal(t, 1, rows)
}

func Test_Open_fails_when_the_parent_path_is_a_file(t *testing.T) {
	// Given: a regular file where the database's parent directory should be.
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(parent, []byte("occupied"), 0o600))

	// When: Open tries to create a database inside it.
	db, err := store.Open(t.Context(), filepath.Join(parent, "cosmics.db"))

	// Then: it fails with a wrapped error and returns no handle.
	require.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "cosmics.db", "the error names the path it could not open")
}

func Test_Open_creates_missing_parent_directories(t *testing.T) {
	// Given: a nested path whose directories do not exist.
	path := filepath.Join(t.TempDir(), "deep", "nested", "state", "cosmics.db")

	// When: Open is called.
	db, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// Then: the file exists and the schema was applied.
	assert.FileExists(t, path)
	assert.Equal(t, path, db.Path())

	var tables int
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'engagements'`).Scan(&tables))
	assert.Equal(t, 1, tables)
}

func Test_Open_rejects_an_empty_path(t *testing.T) {
	// Given: no path at all.

	// When: Open is called.
	db, err := store.Open(t.Context(), "")

	// Then: it refuses rather than creating a mystery database.
	require.Error(t, err)
	assert.Nil(t, db)
}

func Test_Close_is_idempotent_when_called_twice(t *testing.T) {
	// Given: an open database closed once already.
	path := filepath.Join(t.TempDir(), "cosmics.db")
	db, err := store.Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// When: Close runs again.
	second := db.Close()

	// Then: it stays silent - database/sql pools are idempotent to close.
	assert.NoError(t, second)
}
