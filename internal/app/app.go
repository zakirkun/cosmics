// Package app is the composition root. It wires configuration, persistence, and
// the terminal UI together, owns process lifecycle, and exposes the small façade
// the CLI calls. It contains no domain rules of its own.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/tui"
	"github.com/cosmics/cosmics/internal/version"
)

// ErrNoDataDir is returned when no data directory could be resolved.
var ErrNoDataDir = errors.New("app: data directory is not configured")

// DatabaseName is the file name used inside the data directory.
const DatabaseName = "cosmics.db"

// Store wraps the persistence handle with the inspection helpers the CLI needs.
type Store struct {
	*store.DB

	path string
}

// Path returns the absolute database file path.
func (s *Store) Path() string { return s.path }

// Info summarises the database for `cosmics db info`.
type Info struct {
	SchemaVersion int
	TableCount    int
}

// Info reads the applied schema version and the number of user tables.
func (s *Store) Info(ctx context.Context) (Info, error) {
	var info Info

	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
	).Scan(&info.SchemaVersion); err != nil {
		return Info{}, fmt.Errorf("read schema version: %w", err)
	}

	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%'`,
	).Scan(&info.TableCount); err != nil {
		return Info{}, fmt.Errorf("count tables: %w", err)
	}

	return info, nil
}

// OpenStore resolves the data directory and opens the database, applying any
// pending migrations.
func OpenStore(ctx context.Context, cfg config.Config) (*Store, error) {
	path, err := databasePath(cfg)
	if err != nil {
		return nil, err
	}

	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}

	return &Store{DB: db, path: path}, nil
}

// databasePath resolves the configured data directory into a database path.
func databasePath(cfg config.Config) (string, error) {
	dir, err := cfg.DataDir()
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if dir == "" {
		return "", ErrNoDataDir
	}
	return filepath.Join(dir, DatabaseName), nil
}

// Run opens the store, starts the terminal UI, and tears everything down when the
// UI exits. The TUI owns the session; this function owns its lifetime.
func Run(ctx context.Context, cfg config.Config) error {
	db, err := OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore(db)

	engagements, err := store.NewEngagementRepo(db.DB).List(ctx)
	if err != nil {
		return fmt.Errorf("load engagements: %w", err)
	}

	model := tui.New(tui.Options{
		Operator: cfg.App.Operator,
		DataDir:  cfg.App.DataDir,
		Version:  version.String(),
	})

	program := tea.NewProgram(model)

	// Send, not constructor injection: the event loop must be running before the
	// list arrives, or the first frame blocks on database I/O.
	go func() {
		program.Send(tui.EngagementsLoadedMsg{Engagements: engagements})
	}()

	if _, runErr := program.Run(); runErr != nil {
		return fmt.Errorf("run terminal ui: %w", runErr)
	}
	return nil
}

// closeStore releases the database, reporting failure on stderr because the
// caller has already returned by the time it runs.
func closeStore(db *Store) {
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "cosmics: close database: %v\n", err)
	}
}
