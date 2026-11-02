// Package approval owns the human decision that authorizes one specific
// destructive action.
//
// The package is deliberately split in two: gate.go holds the pure decision
// logic (no I/O, no clock, no database), and gate_store.go adapts a store
// repository to the Store port the executor's Authorizer depends on. Keeping
// the decision pure means the "may this run" question is a total function of
// the row and an instant, which is what makes it table-testable.
package approval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors. Callers branch with errors.Is; every one of them means the
// action must not run.
var (
	// ErrNotApproved is returned when no approval exists for the action.
	ErrNotApproved = errors.New("approval: action is not approved")

	// ErrPending is returned while the operator has not answered yet.
	ErrPending = errors.New("approval: decision is still pending")

	// ErrDenied is returned when the operator refused the action.
	ErrDenied = errors.New("approval: action was denied")

	// ErrExpired is returned when the approval's window closed, whether the
	// row says so or the wall clock simply passed expires_at.
	ErrExpired = errors.New("approval: approval has expired")

	// ErrInvalidRequest is returned when a request cannot be persisted or
	// trusted: missing preview, missing reason, bad kind, bad window.
	ErrInvalidRequest = errors.New("approval: invalid request")
)

// Kind classifies what the operator is being asked to authorize.
//
// The four values are exactly the approvals.kind CHECK members. Note the brief
// said "five values" while listing four; the DDL admits four, so a fifth
// constant here would be unwritable and is deliberately absent.
type Kind string

// Approval kinds, matching the approvals CHECK constraint.
const (
	KindDestructiveAction Kind = "destructive_action"
	KindExploitLaunch     Kind = "exploit_launch"
	KindCredentialUse     Kind = "credential_use" //nolint:gosec // G101: an action kind, not a credential
	KindDataAccess        Kind = "data_access"
)

// AllKinds lists every declared kind, for exhaustiveness tests.
var AllKinds = []Kind{
	KindDestructiveAction, KindExploitLaunch, KindCredentialUse, KindDataAccess,
}

// Valid reports whether the kind is a declared constant.
func (k Kind) Valid() bool {
	switch k {
	case KindDestructiveAction, KindExploitLaunch, KindCredentialUse, KindDataAccess:
		return true
	}
	return false
}

// Decision is the operator's answer. It starts pending and moves once.
type Decision string

// Decisions, matching the approvals CHECK constraint.
const (
	DecisionPending  Decision = "pending"
	DecisionApproved Decision = "approved"
	DecisionDenied   Decision = "denied"
	DecisionExpired  Decision = "expired"
)

// AllDecisions lists every declared decision, for exhaustiveness tests.
var AllDecisions = []Decision{
	DecisionPending, DecisionApproved, DecisionDenied, DecisionExpired,
}

// Valid reports whether the decision is a declared constant.
func (d Decision) Valid() bool {
	switch d {
	case DecisionPending, DecisionApproved, DecisionDenied, DecisionExpired:
		return true
	}
	return false
}

// Terminal reports whether the decision is final and can never change.
//
// Pending is the only non-terminal state: the trigger
// trg_approvals_decision_immutable raises on any UPDATE that moves a row out of
// a decided state, so this predicate is the Go side of the same rule.
func (d Decision) Terminal() bool {
	switch d {
	case DecisionApproved, DecisionDenied, DecisionExpired:
		return true
	case DecisionPending:
		return false
	}
	return false
}

// Request is one approval row: the exact thing being authorized, its window,
// and the decision.
type Request struct {
	ID           domain.ID
	EngagementID domain.ID
	ToolRunID    domain.ID
	LLMCallID    domain.ID
	Kind         Kind

	// CommandPreview is the EXACT argv string that would run, not a summary.
	// The operator approves a command, so an approval whose preview does not
	// match the argv is worth nothing.
	CommandPreview string

	// Reason is why the agent believes the action is necessary.
	Reason string

	Decision  Decision
	DecidedAt domain.TimestampMs
	DecidedBy string

	ExpiresAt domain.TimestampMs
	CreatedAt domain.TimestampMs
}

// Validate enforces the contract every persisted request must satisfy.
//
// It checks only what the row owns; identifier and foreign-key presence is the
// repository's concern because a request may be validated before its dependents
// exist.
func (r Request) Validate() error {
	if strings.TrimSpace(r.CommandPreview) == "" {
		return fmt.Errorf("command_preview: %w", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("reason: %w", ErrInvalidRequest)
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("kind %q: %w", r.Kind, ErrInvalidRequest)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("created_at: %w", ErrInvalidRequest)
	}
	if r.ExpiresAt <= r.CreatedAt {
		return fmt.Errorf("expires_at %d must be after created_at %d: %w",
			r.ExpiresAt, r.CreatedAt, ErrInvalidRequest)
	}
	return nil
}

// Check reports whether this request authorizes the action at instant now.
//
// It is pure and total: every (decision, instant) pair returns either nil or
// exactly one sentinel, and there is no third outcome. Approval is a lease, not
// a permanent grant, so an approved row past expires_at is ErrExpired even
// though the row still reads "approved" - the row records the decision, the
// clock decides whether it still binds.
func (r Request) Check(now domain.TimestampMs) error {
	switch r.Decision {
	case DecisionApproved:
		if now >= r.ExpiresAt {
			return fmt.Errorf("approval %s expired at %d: %w", r.ID, r.ExpiresAt, ErrExpired)
		}
		return nil
	case DecisionPending:
		return fmt.Errorf("approval %s is pending: %w", r.ID, ErrPending)
	case DecisionDenied:
		return fmt.Errorf("approval %s was denied: %w", r.ID, ErrDenied)
	case DecisionExpired:
		return fmt.Errorf("approval %s expired at %d: %w", r.ID, r.ExpiresAt, ErrExpired)
	}
	// An undecided-yet-unreadable decision is not approval; refuse it.
	return fmt.Errorf("approval %s has decision %q: %w", r.ID, r.Decision, ErrNotApproved)
}

// IsTerminal reports whether the request has been decided and can no longer
// move.
func (r Request) IsTerminal(now domain.TimestampMs) bool {
	if r.Decision.Terminal() {
		return true
	}
	// A pending row past its deadline is spent even before the sweeper flips
	// it: nobody can approve it in time, so it must not be treated as live.
	return now >= r.ExpiresAt
}
