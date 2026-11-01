package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/store"
)

// ftsShadowSuffixes are the internal tables FTS5 materialises per virtual table.
var ftsShadowSuffixes = []string{"_data", "_idx", "_content", "_docsize", "_config"}

// schemaTables enumerates real tables, skipping FTS5's internal backing tables.
func schemaTables(t *testing.T, db *store.DB) []string {
	t.Helper()

	rows, err := db.Reader().QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		if isFTSShadowTable(name) {
			continue
		}
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

func isFTSShadowTable(name string) bool {
	for _, suffix := range ftsShadowSuffixes {
		if base, ok := strings.CutSuffix(name, suffix); ok && strings.Contains(base, "_fts") {
			return true
		}
	}
	return false
}

func Test_Migrate_creates_every_schema_table_when_database_is_empty(t *testing.T) {
	// Given: a fresh database, migrated by Open.
	db := openTestDB(t)

	expected := []string{
		"access_sessions",
		"approvals",
		"artifacts",
		"assets",
		"attack_edges",
		"attack_nodes",
		"audit_trail",
		"chunks_fts",
		"credentials",
		"engagement_targets",
		"engagements",
		"events",
		"events_fts",
		"evidence",
		"facts",
		"facts_fts",
		"findings",
		"llm_calls",
		"memory_chunks",
		"messages",
		"post_exploit_actions",
		"reports",
		"schema_migrations",
		"services",
		"sessions",
		"skills",
		"stage_runs",
		"tool_defs",
		"tool_runs",
		"usage_ledger",
	}

	// When: the schema's tables are enumerated.
	actual := schemaTables(t, db)

	// Then: the set matches the ERD exactly - no table missing, none extra.
	// 26 ERD tables + schema_migrations + 3 FTS5 virtual tables = 30.
	assert.Equal(t, expected, actual, "schema tables must match the ERD")
	assert.Len(t, actual, 30)
}

func Test_Migrate_is_idempotent_when_run_twice(t *testing.T) {
	// Given: a database already migrated by Open.
	db := openTestDB(t)

	// When: the migrator runs a second time.
	err := store.Migrate(t.Context(), db.Writer())

	// Then: it is a silent no-op and the history still has one row.
	require.NoError(t, err)

	var applied int
	require.NoError(t, db.Reader().
		QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied))
	assert.Equal(t, 1, applied, "0001_init must be recorded exactly once")
}

func Test_Migrate_aborts_when_a_recorded_checksum_changes(t *testing.T) {
	// Given: a migrated database whose recorded checksum has been tampered with
	// to simulate a migration file that changed after it was applied.
	db := openTestDB(t)

	_, err := db.Writer().ExecContext(t.Context(),
		`UPDATE schema_migrations SET checksum = ? WHERE version = 1`,
		"0000000000000000000000000000000000000000000000000000000000000000")
	require.NoError(t, err, "schema_migrations has no append-only trigger")

	// When: the migrator runs again.
	err = store.Migrate(t.Context(), db.Writer())

	// Then: it refuses to proceed and says why.
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrChecksumMismatch)
	assert.NotErrorIs(t, err, store.ErrMigrationOrder)
}

func Test_Migrate_rejects_an_applied_version_that_is_not_embedded(t *testing.T) {
	// Given: a migrated database with an extra history row for a version the
	// binary does not ship.
	db := openTestDB(t)

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		99, "0099_from_the_future.sql", "deadbeef", time.Now().UnixMilli())
	require.NoError(t, err)

	// When: the migrator runs again.
	err = store.Migrate(t.Context(), db.Writer())

	// Then: the history cannot be reconciled with the embedded set.
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrMigrationOrder)
	assert.NotErrorIs(t, err, store.ErrChecksumMismatch)
}

func Test_Migrate_creates_append_only_triggers_when_schema_is_applied(t *testing.T) {
	// Given: a migrated database holding one engagement, one event, one audit
	// row, and one engagement target.
	db := openTestDB(t)
	ctx := t.Context()

	seedAppendOnlyFixture(t, db, ctx)

	rejections := []struct {
		name  string
		query string
	}{
		{"events UPDATE", `UPDATE events SET message = 'rewritten' WHERE id = 'EVT'`},
		{"events DELETE", `DELETE FROM events WHERE id = 'EVT'`},
		{"audit_trail UPDATE", `UPDATE audit_trail SET outcome = 'denied' WHERE id = 'AUD'`},
		{"audit_trail DELETE", `DELETE FROM audit_trail WHERE id = 'AUD'`},
		{"engagements.authorized_by UPDATE", `UPDATE engagements SET authorized_by = 'mallory' WHERE id = 'ENG'`},
		{"engagements.authorized_at UPDATE", `UPDATE engagements SET authorized_at = 1 WHERE id = 'ENG'`},
		{"llm_calls UPDATE", `UPDATE llm_calls SET status = 'ok' WHERE id = 'LLM'`},
		{"llm_calls DELETE", `DELETE FROM llm_calls WHERE id = 'LLM'`},
	}

	for _, tc := range rejections {
		// When: the mutation runs against the writer handle.
		_, err := db.Writer().ExecContext(ctx, tc.query)

		// Then: the database rejects it with a real SQLite error.
		require.Error(t, err, "%s must be rejected", tc.name)

		var sqliteErr sqliteError
		require.ErrorAs(t, err, &sqliteErr, "%s must surface a SQLite error", tc.name)
		assert.NotEqual(t, sqliteCodeOK, sqliteErr.Code(), "%s must not be a silent no-op", tc.name)
	}

	// Then: the guarded values are unchanged.
	var (
		message      string
		authorizedBy string
	)
	require.NoError(t, db.Reader().
		QueryRowContext(ctx, `SELECT message FROM events WHERE id = 'EVT'`).Scan(&message))
	assert.Equal(t, "seed event", message)

	require.NoError(t, db.Reader().
		QueryRowContext(ctx, `SELECT authorized_by FROM engagements WHERE id = 'ENG'`).Scan(&authorizedBy))
	assert.Equal(t, "alice", authorizedBy)
}

// seedAppendOnlyFixture inserts the rows the trigger assertions mutate. It uses
// the writer handle directly because the repositories would reject ULIDs like
// "ENG" and these tests are about triggers, not domain validation.
func seedAppendOnlyFixture(t *testing.T, db *store.DB, ctx context.Context) {
	t.Helper()

	now := time.Now().UTC().UnixMilli()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.Writer().ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed: %s", query)
	}

	exec(`INSERT INTO engagements
		(id, name, client, mode, status, stage, model_profile, authorized_by,
		 authorization_note, authorized_at, created_at, updated_at, config_json)
		VALUES ('ENG', 'fixture', 'acme', 'recon', 'draft', 'none', 'default', 'alice', 'note', ?, ?, ?, '{}')`,
		now, now, now)

	exec(`INSERT INTO sessions
		(id, engagement_id, title, model, system_prompt_hash, status, created_at, updated_at)
		VALUES ('SES', 'ENG', 'fixture', 'model', 'hash', 'open', ?, ?)`, now, now)

	exec(`INSERT INTO events
		(id, engagement_id, stage, kind, level, message, created_at)
		VALUES ('EVT', 'ENG', 'recon', 'note', 'info', 'seed event', ?)`, now)

	exec(`INSERT INTO audit_trail
		(id, engagement_id, actor, action, target, outcome, prev_hash, entry_hash, created_at)
		VALUES ('AUD', 'ENG', 'operator', 'engagement_created', 'fixture', 'allowed', '', 'hash', ?)`, now)

	exec(`INSERT INTO llm_calls
		(id, engagement_id, stage, provider, model_requested, purpose, request_json, status, started_at, finished_at)
		VALUES ('LLM', 'ENG', 'recon', 'openai_compatible', 'model', 'stage_plan', '{}', 'ok', ?, ?)`, now, now)
}
