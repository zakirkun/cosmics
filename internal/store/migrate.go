package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationFile is one embedded migration, already ordered and hashed.
type migrationFile struct {
	version  int
	name     string
	sql      string
	checksum string
}

// appliedMigration is one row of schema_migrations.
type appliedMigration struct {
	version  int
	name     string
	checksum string
}

// schemaMigrationsDDL creates the bookkeeping table. The migrator owns it, so
// 0001_init.sql deliberately does not declare it.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at INTEGER NOT NULL
)`

// Migrate brings db up to the embedded schema.
//
// It creates schema_migrations if absent, loads the embedded files in lexical
// order, and verifies that every already-applied version still hashes to what
// was recorded. A mismatch aborts with ErrChecksumMismatch; a version that is
// recorded but not embedded, missing in the middle of the applied range, or
// claimed twice aborts with ErrMigrationOrder. Each pending file is applied as
// a single Exec inside one transaction that also inserts its bookkeeping row,
// so a failed migration is never marked applied.
func Migrate(ctx context.Context, db *sql.DB) error {
	files, err := loadMigrations()
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := readAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}

	if err := verifyApplied(files, applied); err != nil {
		return err
	}

	for _, file := range files {
		if _, ok := applied[file.version]; ok {
			continue
		}
		if err := applyMigration(ctx, db, file); err != nil {
			return err
		}
	}

	return nil
}

// loadMigrations reads, parses, and hashes every embedded SQL file in lexical
// filename order.
func loadMigrations() ([]migrationFile, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	files := make([]migrationFile, 0, len(names))
	seen := make(map[int]string, len(names))

	for _, name := range names {
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("migrations %q and %q both claim version %d: %w",
				previous, name, version, ErrMigrationOrder)
		}
		seen[version] = name

		body, err := migrationFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return nil, fmt.Errorf("migration %q is empty: %w", name, ErrMigrationOrder)
		}

		sum := sha256.Sum256(body)
		files = append(files, migrationFile{
			version:  version,
			name:     name,
			sql:      string(body),
			checksum: hex.EncodeToString(sum[:]),
		})
	}

	return files, nil
}

// parseMigrationVersion extracts the NNNN prefix from a NN_NN_name.sql file.
func parseMigrationVersion(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found || prefix == "" {
		return 0, fmt.Errorf("migration %q does not start with a numeric version: %w", name, ErrMigrationOrder)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration %q version %q is not a number: %w", name, prefix, ErrMigrationOrder)
	}
	if version <= 0 {
		return 0, fmt.Errorf("migration %q version %d must be positive: %w", name, version, ErrMigrationOrder)
	}
	return version, nil
}

// readAppliedMigrations loads the recorded history keyed by version.
func readAppliedMigrations(ctx context.Context, db *sql.DB) (map[int]appliedMigration, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]appliedMigration)
	for rows.Next() {
		var row appliedMigration
		if err := rows.Scan(&row.version, &row.name, &row.checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		applied[row.version] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// verifyApplied rejects a history that cannot be reconciled with the embedded
// set: an unknown version, a checksum that no longer matches, or a hole in the
// applied sequence.
func verifyApplied(files []migrationFile, applied map[int]appliedMigration) error {
	byVersion := make(map[int]migrationFile, len(files))
	for _, file := range files {
		byVersion[file.version] = file
	}

	for version, row := range applied {
		file, ok := byVersion[version]
		if !ok {
			return fmt.Errorf("version %d (%s) is recorded as applied but is not embedded: %w",
				version, row.name, ErrMigrationOrder)
		}
		if row.checksum != file.checksum {
			return fmt.Errorf("version %d (%s): recorded checksum %s, embedded file hashes to %s: %w",
				version, row.name, row.checksum, file.checksum, ErrChecksumMismatch)
		}
	}

	return nil
}

// applyMigration runs one file and records it in the same transaction.
//
// The file is passed to a single Exec. It must never be split on ';' - a
// CREATE TRIGGER body contains internal semicolons.
func applyMigration(ctx context.Context, db *sql.DB, file migrationFile) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", file.name, err)
	}

	if _, err := tx.ExecContext(ctx, file.sql); err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(fmt.Errorf("rollback migration %s: %w", file.name, rollbackErr),
				fmt.Errorf("apply migration %s: %w", file.name, err))
		}
		return fmt.Errorf("apply migration %s: %w", file.name, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		file.version, file.name, file.checksum, time.Now().UTC().UnixMilli(),
	); err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(fmt.Errorf("rollback migration %s: %w", file.name, rollbackErr),
				fmt.Errorf("record migration %s: %w", file.name, err))
		}
		return fmt.Errorf("record migration %s: %w", file.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", file.name, err)
	}
	return nil
}
