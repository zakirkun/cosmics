package report

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinel errors for verification.
var (
	ErrNoEvidence         = errors.New("report: no proof-of-concept evidence recorded")
	ErrEvidenceIncomplete = errors.New("report: evidence is incomplete")
)

// Artifact is one captured proof-of-concept file.
type Artifact struct {
	ID     domain.ID
	Kind   string
	Path   string
	SHA256 string
	Bytes  int64
}

// RunOutcome is the terminal state of a producing run.
type RunOutcome struct {
	Status   string
	ExitCode int
}

// EvidenceStore is the read surface verification needs.
//
// It is deliberately narrow: a verification may look at evidence and at a run's
// exit state, and nothing else.
type EvidenceStore interface {
	ListRunEvidence(ctx context.Context, runID domain.ID) ([]Artifact, error)
	RunOutcome(ctx context.Context, runID domain.ID) (RunOutcome, error)
}

// FindingStatusWriter is the minimal write surface for marking a finding verified.
type FindingStatusWriter interface {
	SetVerified(ctx context.Context, id domain.ID, verified bool) error
}

// Verification is the outcome of checking one finding.
type Verification struct {
	Verified bool
	Reason   string
}

// VerifyFinding decides whether a finding is proven.
//
// This is the rule the exit criteria depend on: verified means a proof-of-concept
// artifact exists AND the run that produced it finished with status succeeded and
// exit code 0. A model's textual claim of success has no path into this function,
// so it can never set the flag.
func VerifyFinding(ctx context.Context, evidence EvidenceStore, runID domain.ID) (Verification, error) {
	if evidence == nil {
		return Verification{}, fmt.Errorf("evidence store is required: %w", ErrNoEvidence)
	}

	artifacts, err := evidence.ListRunEvidence(ctx, runID)
	if err != nil {
		return Verification{}, fmt.Errorf("list evidence for run %s: %w", runID, err)
	}
	if len(artifacts) == 0 {
		return Verification{Reason: "no proof-of-concept evidence recorded"}, nil
	}

	for _, artifact := range artifacts {
		if artifact.Bytes <= 0 || artifact.SHA256 == "" {
			return Verification{Reason: "evidence is incomplete: missing digest or empty artifact"}, nil
		}
	}

	outcome, err := evidence.RunOutcome(ctx, runID)
	if errors.Is(err, store.ErrToolRunMissing) {
		return Verification{Reason: "producing run is missing"}, nil
	}
	if err != nil {
		return Verification{}, fmt.Errorf("read outcome of run %s: %w", runID, err)
	}
	if outcome.Status != string(store.RunSucceeded) {
		return Verification{
			Reason: fmt.Sprintf("producing run finished %q, not succeeded", outcome.Status),
		}, nil
	}
	if outcome.ExitCode != 0 {
		return Verification{
			Reason: fmt.Sprintf("producing run exited %d, not zero", outcome.ExitCode),
		}, nil
	}

	return Verification{Verified: true, Reason: "evidence exists and the run exited zero"}, nil
}

// MarkVerified sets verified only when VerifyFinding agrees.
//
// Returning the verification alongside the error lets a caller record why a finding
// stayed unverified, which is the information an operator needs to act on it.
func MarkVerified(
	ctx context.Context,
	evidence EvidenceStore,
	findings FindingStatusWriter,
	findingID domain.ID,
	runID domain.ID,
) (Verification, error) {
	verification, err := VerifyFinding(ctx, evidence, runID)
	if err != nil {
		return Verification{}, err
	}
	if !verification.Verified {
		return verification, nil
	}

	if findings == nil {
		return Verification{}, fmt.Errorf("findings writer is required: %w", ErrNoEvidence)
	}
	if err := findings.SetVerified(ctx, findingID, true); err != nil {
		return Verification{}, fmt.Errorf("mark finding %s verified: %w", findingID, err)
	}
	return verification, nil
}
