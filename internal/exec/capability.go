package exec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/tools"
)

// Sentinel errors for the authorization path.
var (
	// ErrNotApproved means the action requires approval and none was supplied.
	ErrNotApproved = errors.New("exec: destructive action is not approved")

	// ErrApprovalInvalid means an approval was supplied but does not authorize
	// the action now: pending, denied, expired, or for a different engagement.
	ErrApprovalInvalid = errors.New("exec: approval does not authorize this action")
)

// ApprovalRow is the subset of an approval this package needs.
//
// It is a local shape rather than the store type so the decision stays a pure
// function of the values it is given, and so a test can build one in a literal.
type ApprovalRow struct {
	ID             domain.ID
	EngagementID   domain.ID
	CommandPreview string
	Decision       string
	ExpiresAt      domain.TimestampMs
}

// ApprovalLookup reads an approval by identifier.
type ApprovalLookup interface {
	GetApproval(ctx context.Context, id domain.ID) (ApprovalRow, error)
}

// RejectionRecorder persists a refused run and returns its identifier.
type RejectionRecorder interface {
	RecordRejection(ctx context.Context, run store.ToolRunRow, reason string) (domain.ID, error)
}

// Authorizer decides whether a spec may run.
//
// The zero value is not usable: an Authorizer without a lookup cannot approve a
// destructive action, which is the right failure mode if one is ever constructed
// with the struct literal by mistake.
type Authorizer struct {
	approvals  ApprovalLookup
	rejections RejectionRecorder
	clock      domain.Clock
}

// AuthorizerOptions configures an Authorizer.
type AuthorizerOptions struct {
	Approvals  ApprovalLookup
	Rejections RejectionRecorder
	Clock      domain.Clock
}

// NewAuthorizer returns an authorizer.
func NewAuthorizer(opts AuthorizerOptions) (*Authorizer, error) {
	if opts.Approvals == nil {
		return nil, fmt.Errorf("approvals lookup: %w", ErrApprovalInvalid)
	}
	if opts.Clock == nil {
		return nil, fmt.Errorf("clock: %w", ErrApprovalInvalid)
	}
	return &Authorizer{
		approvals:  opts.Approvals,
		rejections: opts.Rejections,
		clock:      opts.Clock,
	}, nil
}

// Authorize reports whether the tool may run with this approval.
//
// A safe or intrusive tool needs no approval. A destructive tool needs an
// approval that is approved, unexpired, and issued for the same command preview
// and engagement: approving one command must not authorize a different one, which
// is why the preview is compared rather than ignored.
func (a *Authorizer) Authorize(
	ctx context.Context,
	tool tools.Tool,
	engagementID domain.ID,
	spec RunSpec,
	approvalID domain.ID,
) error {
	if tool.DangerLevel != tools.DangerDestructive && !tool.RequiresApproval {
		return nil
	}

	if approvalID.IsZero() {
		return fmt.Errorf("tool %q is %s: %w", tool.ID, tool.DangerLevel, ErrNotApproved)
	}

	approval, err := a.approvals.GetApproval(ctx, approvalID)
	if err != nil {
		return fmt.Errorf("read approval %s: %w: %w", approvalID, ErrApprovalInvalid, err)
	}

	if approval.EngagementID != engagementID {
		return fmt.Errorf("approval %s belongs to engagement %s: %w",
			approvalID, approval.EngagementID, ErrApprovalInvalid)
	}

	if err := checkApproval(approval, a.clock.Now()); err != nil {
		return fmt.Errorf("approval %s: %w", approvalID, err)
	}

	preview, err := previewFor(spec)
	if err != nil {
		return fmt.Errorf("render command preview: %w", err)
	}
	if preview != approval.CommandPreview {
		return fmt.Errorf("approval %s authorizes %q, not %q: %w",
			approvalID, approval.CommandPreview, preview, ErrApprovalInvalid)
	}

	return nil
}

// checkApproval applies the decision and deadline rules to a row.
func checkApproval(approval ApprovalRow, now domain.TimestampMs) error {
	switch approval.Decision {
	case "approved":
		if now >= approval.ExpiresAt {
			return fmt.Errorf("expired at %d: %w", approval.ExpiresAt, ErrApprovalInvalid)
		}
		return nil
	case "pending":
		return fmt.Errorf("still pending: %w", ErrApprovalInvalid)
	case "denied":
		return fmt.Errorf("was denied: %w", ErrApprovalInvalid)
	case "expired":
		return fmt.Errorf("expired: %w", ErrApprovalInvalid)
	}
	return fmt.Errorf("has decision %q: %w", approval.Decision, ErrApprovalInvalid)
}

// previewFor renders the exact command the run would execute.
//
// The preview is the shell-quoted display form because that is what an operator
// can read and compare. The argv actually executed is the unquoted slice; the two
// describe the same command.
func previewFor(spec RunSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}

	quoted := make([]string, 0, len(spec.Argv))
	for _, token := range spec.Argv {
		quoted = append(quoted, quoteToken(token))
	}
	return strings.Join(quoted, " "), nil
}

func quoteToken(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\n\"'\\$`;&|<>()*?[]{}!#~") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", `'\''`) + "'"
}

// RecordRejection persists a refused run and returns its identifier.
//
// A rejection is recorded even when nothing was started: the fact that an action
// was attempted and refused belongs in the engagement's history.
func (a *Authorizer) RecordRejection(
	ctx context.Context,
	row store.ToolRunRow,
	reason string,
) (domain.ID, error) {
	if a.rejections == nil {
		return domain.ID{}, fmt.Errorf("no rejection recorder configured: %w", ErrNotApproved)
	}

	stored, err := a.rejections.RecordRejection(ctx, row, reason)
	if err != nil {
		return domain.ID{}, fmt.Errorf("record rejection: %w", err)
	}
	return stored, nil
}
