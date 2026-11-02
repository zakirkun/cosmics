// Package cli parses command-line arguments and dispatches to the requested
// command. It owns flag parsing and exit behaviour; everything else lives in the
// packages it calls.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cosmics/cosmics/internal/app"
	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/doctor"
	"github.com/cosmics/cosmics/internal/onboarding"
	"github.com/cosmics/cosmics/internal/version"
)

// Sentinel errors reported to the caller of Run.
var (
	ErrUnknownCommand = errors.New("cli: unknown command")
	ErrMissingConfig  = errors.New("cli: no configuration found")
)

// Run parses args and executes the selected command. It returns an error rather
// than exiting so the caller controls the process status.
func Run(args []string) error {
	flags := flag.NewFlagSet("cosmics", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var (
		configPath  = flags.String("config", "", "path to config.yaml (default: ~/.cosmics/config.yaml)")
		dataDir     = flags.String("data-dir", "", "override the data directory")
		showVersion = flags.Bool("version", false, "print version and exit")
		debug       = flags.Bool("debug", false, "enable debug logging")
	)

	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Cosmics — terminal-native penetration-testing platform\n\n")
		fmt.Fprintf(flags.Output(), "Usage:\n  cosmics [flags] [command]\n\n")
		fmt.Fprintf(flags.Output(), "Commands:\n  (none)   launch the interactive TUI\n  setup    run the first-run wizard and write ~/.cosmics/config.yaml\n  run      execute an engagement through the pipeline\n  report   generate or regenerate a report (markdown or html)\n  audit    verify the append-only audit chain\n  doctor   verify configuration, database, Docker, and the LLM gateway\n  db       inspect the local database\n\n")
		fmt.Fprintf(flags.Output(), "Flags:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if *showVersion {
		_, err := fmt.Fprintln(flags.Output(), version.String())
		return err
	}

	rest := flags.Args()
	command := ""
	if len(rest) > 0 {
		command = rest[0]
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if command == "setup" {
		return runSetup(ctx, rest[1:], flags.Output())
	}
	if !knownCommand(command) {
		return fmt.Errorf("command %q: %w", command, ErrUnknownCommand)
	}

	cfg, err := loadConfig(ctx, *configPath, *dataDir)
	if err != nil {
		return err
	}
	if *debug {
		cfg.App.LogLevel = "debug"
	}

	switch command {
	case "":
		return runTUI(ctx, cfg)
	case "doctor":
		return runDoctor(ctx, cfg, flags.Output())
	case "db":
		return runDB(ctx, cfg, rest[1:])
	case "report":
		return runReport(ctx, cfg, rest[1:], flags.Output())
	case "audit":
		return runAudit(ctx, cfg, rest[1:], flags.Output())
	case "run":
		return runEngagement(ctx, cfg, rest[1:], flags.Output())
	}
	return nil
}

func knownCommand(command string) bool {
	switch command {
	case "", "setup", "doctor", "db", "report", "audit", "run":
		return true
	}
	return false
}

// loadConfig prefers an explicit path, then the default data directory, then
// the environment. A first run with nothing configured is sent through the
// setup wizard instead of silently guessing a gateway.
func loadConfig(ctx context.Context, explicitPath, dataDirOverride string) (config.Config, error) {
	if explicitPath != "" {
		cfg, err := config.Load(ctx, explicitPath)
		if err != nil {
			return config.Config{}, fmt.Errorf("load config %s: %w", explicitPath, err)
		}
		return cfg, nil
	}

	dir := dataDirOverride
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return config.Config{}, fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".cosmics")
	}

	defaultPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(defaultPath); err == nil {
		cfg, loadErr := config.Load(ctx, defaultPath)
		if loadErr != nil {
			return config.Config{}, fmt.Errorf("load config %s: %w", defaultPath, loadErr)
		}
		return cfg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, fmt.Errorf("stat config %s: %w", defaultPath, err)
	}

	cfg, err := config.LoadFromEnv(ctx)
	if err != nil {
		return config.Config{}, fmt.Errorf("load config from environment: %w", err)
	}
	if hasUsableProvider(cfg) || !stdinIsTerminal() {
		cfg.App.DataDir = dir
		return cfg, nil
	}

	cfg, err = onboardFirstRun(ctx, dir, os.Stdin, os.Stderr, onboarding.GatewayDiscoverer{})
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// stdinIsTerminal gates the first-run wizard: prompting a piped or closed
// stdin would block a script forever, so a non-interactive run keeps the
// environment fallback.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// runTUI starts the interactive application.
func runTUI(ctx context.Context, cfg config.Config) error {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()

	if err := app.Run(ctx, cfg); err != nil {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}

// runDoctor prints a health report and returns a non-nil error when any check
// fails, so CI and scripts can gate on it.
func runDoctor(ctx context.Context, cfg config.Config, out io.Writer) error {
	report, err := doctor.Run(ctx, cfg)
	if err != nil {
		return fmt.Errorf("run diagnostics: %w", err)
	}
	if _, err := fmt.Fprint(out, report.Render()); err != nil {
		return fmt.Errorf("write doctor report: %w", err)
	}
	if !report.Healthy() {
		return doctor.ErrUnhealthy
	}
	return nil
}

// runDB handles database subcommands.
func runDB(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("db: expected a subcommand (path|info): %w", ErrUnknownCommand)
	}
	db, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "cosmics: close database: %v\n", closeErr)
		}
	}()

	switch args[0] {
	case "path":
		_, err := fmt.Fprintln(os.Stdout, db.Path())
		return err
	case "info":
		info, infoErr := db.Info(ctx)
		if infoErr != nil {
			return fmt.Errorf("read database info: %w", infoErr)
		}
		_, err := fmt.Fprintf(os.Stdout, "path: %s\nschema version: %d\ntables: %d\n", db.Path(), info.SchemaVersion, info.TableCount)
		return err
	default:
		return fmt.Errorf("db subcommand %q: %w", args[0], ErrUnknownCommand)
	}
}
