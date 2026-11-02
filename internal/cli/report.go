package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmics/cosmics/internal/app"
	"github.com/cosmics/cosmics/internal/clock"
	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/report"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinels reported by the report command.
var (
	ErrReportFailed        = errors.New("cli: report failed")
	ErrEngagementNotFound  = errors.New("cli: engagement not found")
	ErrReportFormatUnknown = errors.New("cli: unknown report format")
	ErrReportOutputExists  = errors.New("cli: report output already exists")
)

// Report formats accepted on the command line.
const (
	formatMarkdown = "markdown"
	formatHTML     = "html"
)

// fileMode is owner-only: a report is client material, not world-readable.
const fileMode = 0o600

// runReport dispatches the report subcommands.
func runReport(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("report: expected a subcommand (generate|regenerate): %w", ErrUnknownCommand)
	}

	switch args[0] {
	case "generate":
		return generateReport(ctx, cfg, args[1:], out, clock.NewSystem())
	case "regenerate":
		return regenerateReport(ctx, cfg, args[1:], out, clock.NewSystem())
	default:
		return fmt.Errorf("report subcommand %q: %w", args[0], ErrUnknownCommand)
	}
}

// generateReport writes a new report and refuses to clobber an existing file.
func generateReport(ctx context.Context, cfg config.Config, args []string, out io.Writer, clk domain.Clock) error {
	return writeReport(ctx, cfg, args, out, clk, false)
}

// regenerateReport rewrites an existing report so two runs can be compared.
func regenerateReport(ctx context.Context, cfg config.Config, args []string, out io.Writer, clk domain.Clock) error {
	return writeReport(ctx, cfg, args, out, clk, true)
}

// writeReport is the shared body of generate and regenerate.
func writeReport(
	ctx context.Context,
	cfg config.Config,
	args []string,
	out io.Writer,
	clk domain.Clock,
	overwrite bool,
) error {
	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	flags.SetOutput(out)
	engagementID := flags.String("engagement", "", "engagement id to report on")
	format := flags.String("format", formatMarkdown, "markdown or html")
	path := flags.String("out", "", "file to write")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse report flags: %w", err)
	}
	if strings.TrimSpace(*engagementID) == "" {
		return fmt.Errorf("report: --engagement is required: %w", ErrReportFailed)
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("report: --out is required: %w", ErrReportFailed)
	}

	id, err := domain.NewID(*engagementID)
	if err != nil {
		return fmt.Errorf("engagement id %q: %w", *engagementID, err)
	}

	renderer, resolvedFormat, err := reportRenderer(*format, *path)
	if err != nil {
		return err
	}

	db, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer closeStoreQuietly(db)

	document, err := report.Build(ctx, report.Deps{
		Engagements: store.NewEngagementRepo(db.DB),
		Findings:    store.NewFindingRepo(db.DB),
		Assets:      store.NewAssetRepo(db.DB),
		Clock:       clk,
	}, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("engagement %q: %w", id, ErrEngagementNotFound)
		}
		return fmt.Errorf("build report for %q: %w", id, err)
	}

	payload := renderer(document)
	if err := writeReportFile(*path, payload, overwrite); err != nil {
		return err
	}

	digest := sha256.Sum256(payload)
	_, err = fmt.Fprintf(out,
		"engagement: %s (%s)\nformat: %s\nwrote: %s\nbytes: %d\nsha256: %s\n",
		document.Name, document.EngagementID, resolvedFormat, *path, len(payload),
		hex.EncodeToString(digest[:]))
	if err != nil {
		return fmt.Errorf("write report summary: %w", err)
	}
	return nil
}

// reportRenderer resolves the format and returns the matching pure renderer.
func reportRenderer(format, path string) (func(report.Report) []byte, string, error) {
	resolved := strings.ToLower(strings.TrimSpace(format))
	if resolved == "" {
		resolved = inferredFormat(path)
	}

	switch resolved {
	case formatMarkdown, "md":
		return report.RenderMarkdown, formatMarkdown, nil
	case formatHTML, "htm":
		return report.RenderHTML, formatHTML, nil
	default:
		return nil, "", fmt.Errorf("format %q: %w", format, ErrReportFormatUnknown)
	}
}

// inferredFormat guesses from the file extension, defaulting to markdown.
func inferredFormat(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".html") {
		return formatHTML
	}
	return formatMarkdown
}

// writeReportFile writes the bytes, refusing to replace a file unless asked.
func writeReportFile(path string, payload []byte, overwrite bool) error {
	if !overwrite {
		_, err := os.Stat(path)
		switch {
		case err == nil:
			return fmt.Errorf("%s: use report regenerate: %w", path, ErrReportOutputExists)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	if err := os.WriteFile(path, payload, fileMode); err != nil {
		return fmt.Errorf("write report %s: %w", path, err)
	}
	return nil
}

// closeStoreQuietly releases the database, reporting failure on stderr because
// the caller has already produced its result.
func closeStoreQuietly(db *app.Store) {
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "cosmics: close database: %v\n", err)
	}
}
