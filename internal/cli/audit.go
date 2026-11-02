package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/cosmics/cosmics/internal/app"
	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinels reported by the audit command.
var (
	ErrAuditFailed = errors.New("cli: audit verification failed")
	ErrAuditBroken = errors.New("cli: audit hash chain is broken")
)

// auditResult is the machine-readable form of one verification.
type auditResult struct {
	Verified bool   `json:"verified"`
	Count    int    `json:"count"`
	BrokenID string `json:"broken_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// runAudit dispatches the audit subcommands.
func runAudit(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("audit: expected a subcommand (verify): %w", ErrUnknownCommand)
	}

	switch args[0] {
	case "verify":
		return verifyAudit(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("audit subcommand %q: %w", args[0], ErrUnknownCommand)
	}
}

// verifyAudit re-walks the hash chain and reports its verdict.
func verifyAudit(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print one JSON object instead of prose")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse audit flags: %w", err)
	}

	db, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer closeStoreQuietly(db)

	result, verifyErr := verifyChain(ctx, store.NewAuditRepo(db.DB))
	if writeErr := emitAuditResult(out, result, *asJSON); writeErr != nil {
		return writeErr
	}
	return verifyErr
}

// verifyChain converts the store's verification into the command's result,
// separating "the walk failed" from "the chain is broken".
func verifyChain(ctx context.Context, repo *store.AuditRepo) (auditResult, error) {
	verified, err := repo.Verify(ctx)
	if err == nil {
		return auditResult{Verified: true, Count: verified}, nil
	}

	var broken *store.AuditVerifyError
	if errors.As(err, &broken) {
		return auditResult{
				Verified: false,
				Count:    verified,
				BrokenID: broken.RowID,
				Reason:   broken.Reason,
			},
			fmt.Errorf("audit row %s: %w: %s", broken.RowID, ErrAuditBroken, broken.Reason)
	}
	return auditResult{Verified: false, Count: verified},
		fmt.Errorf("verify audit chain: %w: %w", ErrAuditFailed, err)
}

// emitAuditResult writes the verdict in the requested encoding.
func emitAuditResult(out io.Writer, result auditResult, asJSON bool) error {
	if asJSON {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("encode audit result: %w", err)
		}
		if _, err := fmt.Fprintf(out, "%s\n", payload); err != nil {
			return fmt.Errorf("write audit result: %w", err)
		}
		return nil
	}

	if result.Verified {
		_, err := fmt.Fprintf(out, "audit chain verified: %d row(s)\n", result.Count)
		if err != nil {
			return fmt.Errorf("write audit result: %w", err)
		}
		return nil
	}

	_, err := fmt.Fprintf(out,
		"audit chain broken at row %s after %d verified row(s): %s\n",
		result.BrokenID, result.Count, result.Reason)
	if err != nil {
		return fmt.Errorf("write audit result: %w", err)
	}
	return nil
}
