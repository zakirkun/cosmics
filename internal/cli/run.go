package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/cosmics/cosmics/internal/app"
	"github.com/cosmics/cosmics/internal/clock"
	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// ErrEngagementNotRunnable reports a run refused because of the engagement's
// status.
var ErrEngagementNotRunnable = errors.New("cli: engagement is not runnable")

// runEngagement runs one engagement through the pipeline.
//
// Refusing a non-runnable status is not a formality: a completed engagement re-run
// would duplicate findings and attack edges the operator already reviewed, so the
// status is checked before any stage is constructed.
func runEngagement(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(out)

	engagementID := flags.String("engagement", "", "engagement identifier (required)")
	dataDir := flags.String("data-dir", "", "override the data directory")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse run flags: %w", err)
	}
	if strings.TrimSpace(*engagementID) == "" {
		return fmt.Errorf("--engagement is required: %w", ErrUnknownCommand)
	}
	if *dataDir != "" {
		cfg.App.DataDir = *dataDir
	}

	path, err := cfg.DataDir()
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	_ = path

	db, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer closeStoreQuietly(db)

	id, err := domain.NewID(*engagementID)
	if err != nil {
		return fmt.Errorf("engagement id %q: %w", *engagementID, err)
	}

	engagement, err := store.NewEngagementRepo(db.DB).Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read engagement %s: %w", *engagementID, err)
	}

	if !runnableStatus(engagement.Status) {
		return fmt.Errorf("engagement %s is %q, not ready or paused: %w",
			engagement.ID, engagement.Status, ErrEngagementNotRunnable)
	}

	_, err = fmt.Fprintf(out, "Running %s in %s mode from stage %s\n",
		engagement.Name, engagement.Mode, engagement.Stage)
	if err != nil {
		return fmt.Errorf("write run header: %w", err)
	}

	finished, runErr := app.RunEngagement(ctx, db.DB, clock.NewSystem(), engagement,
		func(stage domain.StageKind) {
			_, _ = fmt.Fprintf(out, "  → %s\n", stage)
		})
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_, writeErr := fmt.Fprintf(out, "Interrupted at stage %s\n", finished.Stage)
			if writeErr != nil {
				return fmt.Errorf("write interruption: %w", writeErr)
			}
			return fmt.Errorf("run cancelled: %w", runErr)
		}
		return fmt.Errorf("run engagement: %w", runErr)
	}

	_, err = fmt.Fprintf(out, "Finished %s at stage %s with status %s\n",
		finished.Name, finished.Stage, finished.Status)
	return err
}

// runnableStatus reports whether an engagement may be executed.
//
// Ready is the normal entry. Paused is accepted because resuming is the whole
// point of pausing; Running is refused so two processes cannot drive one
// engagement at once.
func runnableStatus(status domain.Status) bool {
	switch status {
	case domain.StatusReady, domain.StatusPaused:
		return true
	case domain.StatusDraft, domain.StatusRunning, domain.StatusCompleted,
		domain.StatusFailed, domain.StatusAborted:
		return false
	}
	return false
}
