package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

// Pool sizes. One writer serializes every mutation through the pool, so SQLite
// never has to arbitrate between two competing writer connections; four readers
// exploit WAL's ability to read while a write is in flight.
const (
	writerMaxOpenConns = 1
	readerMaxOpenConns = 4

	// busyRetryAttempts is the number of retries after the first busy failure,
	// giving four total attempts.
	busyRetryAttempts = 3
)

// busyBackoff is the delay before each retry. The schedule grows geometrically
// so a short-lived lock clears on attempt 1 and a long one still resolves
// without spinning.
var busyBackoff = [...]time.Duration{
	50 * time.Millisecond,
	150 * time.Millisecond,
	450 * time.Millisecond,
}

// DB owns both connection handles over one SQLite file.
//
// The zero value is not usable; construct it with Open. Methods are safe for
// concurrent use because *sql.DB is, and the writer pool serializes writes.
type DB struct {
	writer *sql.DB
	reader *sql.DB
	path   string
}

// Open creates the parent directory, opens the writer and reader handles over
// path, proves both respond, and applies the embedded migrations.
//
// A path that cannot be written fails here rather than on the first insert, so
// startup reports the real problem.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("open database: path must not be empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path %q: %w", path, err)
	}

	if dir := filepath.Dir(absPath); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create directory for database %q: %w", absPath, err)
		}
	}

	dsn := dsnFor(absPath)

	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open writer handle: %w", err)
	}
	writer.SetMaxOpenConns(writerMaxOpenConns)
	writer.SetMaxIdleConns(writerMaxOpenConns)
	writer.SetConnMaxLifetime(0)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		closeQuietly(writer)
		return nil, fmt.Errorf("open reader handle: %w", err)
	}
	reader.SetMaxOpenConns(readerMaxOpenConns)
	reader.SetMaxIdleConns(readerMaxOpenConns)
	reader.SetConnMaxLifetime(0)

	if err := writer.PingContext(ctx); err != nil {
		closeQuietly(writer, reader)
		return nil, fmt.Errorf("ping writer handle for %q: %w", absPath, err)
	}
	if err := reader.PingContext(ctx); err != nil {
		closeQuietly(writer, reader)
		return nil, fmt.Errorf("ping reader handle for %q: %w", absPath, err)
	}

	db := &DB{writer: writer, reader: reader, path: absPath}

	if err := Migrate(ctx, writer); err != nil {
		closeQuietly(writer, reader)
		return nil, fmt.Errorf("migrate %q: %w", absPath, err)
	}

	return db, nil
}

// dsnFor builds the connection string for an absolute file path.
//
// url.Values is deliberately not used: it percent-encodes the pragma
// parentheses, and '?'/'#' are the only characters that terminate a SQLite URI
// path (spaces in a Windows home directory are legal unescaped).
func dsnFor(absPath string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(absPath))
	return "file:" + escaped + "?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=temp_store(MEMORY)"
}

// Path returns the absolute path of the database file.
func (db *DB) Path() string { return db.path }

// Reader returns the read pool. It is never used for writes.
func (db *DB) Reader() *sql.DB { return db.reader }

// Writer returns the single-connection writer handle.
//
// Because its pool holds exactly one connection, a query issued while a
// *sql.Rows from the same handle is still open deadlocks forever. Scan into a
// slice and close the rows before issuing the next statement.
func (db *DB) Writer() *sql.DB { return db.writer }

// Close closes both handles, reporting both failures if both occur.
func (db *DB) Close() error {
	return errors.Join(db.writer.Close(), db.reader.Close())
}

// Healthy runs a cheap query against both handles so a caller can distinguish
// "the process is up" from "the database answers".
func (db *DB) Healthy(ctx context.Context) error {
	if err := db.writer.PingContext(ctx); err != nil {
		return fmt.Errorf("health check writer: %w", err)
	}
	var one int
	if err := db.writer.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("health check writer query: %w", err)
	}
	if err := db.reader.PingContext(ctx); err != nil {
		return fmt.Errorf("health check reader: %w", err)
	}
	if err := db.reader.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("health check reader query: %w", err)
	}
	return nil
}

// Tx runs fn inside a write transaction, retrying only when SQLite reports
// SQLITE_BUSY.
//
// fn may be invoked more than once, so it must be free of side effects outside
// the transaction it is handed. A returned error rolls the transaction back and
// is propagated unchanged, wrapped with which attempt failed.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	var lastErr error

	for attempt := range busyRetryAttempts + 1 {
		if attempt > 0 {
			delay := busyBackoff[min(attempt-1, len(busyBackoff)-1)]
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("tx retry %d: %w", attempt, ctx.Err())
			case <-timer.C:
			}
		}

		err := db.runTx(ctx, fn)
		if err == nil {
			return nil
		}
		if !isBusy(err) {
			return err
		}
		lastErr = err
	}

	return fmt.Errorf("tx after %d attempts: %w", busyRetryAttempts+1, lastErr)
}

// runTx performs exactly one begin/commit cycle.
func (db *DB) runTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	if err := fn(tx); err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(fmt.Errorf("rollback tx: %w", rollbackErr), err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(fmt.Errorf("rollback after failed commit: %w", rollbackErr), fmt.Errorf("commit tx: %w", err))
		}
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// isBusy reports whether the error chain contains a SQLITE_BUSY result.
//
// The driver returns *sqlite.Error whose Code() is the primary result code.
// SQLITE_BUSY_* extended codes (snapshot, recovery) mask to SQLITE_BUSY after
// clearing the high extension byte, so the check covers both forms.
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	const sqliteBusy = 5
	const sqlitePrimaryMask = 0xff
	return sqliteErr.Code()&sqlitePrimaryMask == sqliteBusy
}

// closeQuietly closes handles during a failed Open, where the original error is
// the one worth reporting.
func closeQuietly(handles ...*sql.DB) {
	for _, h := range handles {
		if h != nil {
			_ = h.Close()
		}
	}
}
