package report_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/report"
)

// stubEvidence serves configured evidence and outcomes.
type stubEvidence struct {
	artifacts []report.Artifact
	outcome   report.RunOutcome
	listErr   error
	lookedUp  bool
}

func (s *stubEvidence) ListRunEvidence(_ context.Context, _ domain.ID) ([]report.Artifact, error) {
	s.lookedUp = true
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.artifacts, nil
}

func (s *stubEvidence) RunOutcome(_ context.Context, _ domain.ID) (report.RunOutcome, error) {
	return s.outcome, nil
}

// stubFindings records verification writes.
type stubFindings struct {
	marked []bool
	calls  int
}

func (s *stubFindings) SetVerified(_ context.Context, _ domain.ID, verified bool) error {
	s.calls++
	s.marked = append(s.marked, verified)
	return nil
}

// evidenceArtifact returns one complete artifact.
func evidenceArtifact() report.Artifact {
	return report.Artifact{
		ID:     domain.NewULIDGenerator().New(),
		Kind:   "poc",
		Path:   "evidence/poc.txt",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Bytes:  512,
	}
}

func Test_VerifyFinding_is_false_when_no_poc_evidence_exists(t *testing.T) {
	// Given
	evidence := &stubEvidence{}

	// When
	verification, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.False(t, verification.Verified)
	assert.Contains(t, verification.Reason, "no proof-of-concept")
}

func Test_VerifyFinding_is_false_when_the_evidence_is_incomplete(t *testing.T) {
	// Given
	artifact := evidenceArtifact()
	artifact.SHA256 = ""
	evidence := &stubEvidence{artifacts: []report.Artifact{artifact}}

	// When
	verification, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.False(t, verification.Verified)
	assert.Contains(t, verification.Reason, "incomplete")
}

func Test_VerifyFinding_is_false_when_the_run_status_is_not_succeeded(t *testing.T) {
	// Given
	evidence := &stubEvidence{
		artifacts: []report.Artifact{evidenceArtifact()},
		outcome:   report.RunOutcome{Status: "failed", ExitCode: 0},
	}

	// When
	verification, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.False(t, verification.Verified)
	assert.Contains(t, verification.Reason, "failed")
}

func Test_VerifyFinding_is_false_when_the_run_exited_nonzero(t *testing.T) {
	// Given
	evidence := &stubEvidence{
		artifacts: []report.Artifact{evidenceArtifact()},
		outcome:   report.RunOutcome{Status: "succeeded", ExitCode: 2},
	}

	// When
	verification, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.False(t, verification.Verified)
	assert.Contains(t, verification.Reason, "2")
}

func Test_VerifyFinding_is_true_only_when_evidence_exists_and_the_run_exited_zero(t *testing.T) {
	// Given
	evidence := &stubEvidence{
		artifacts: []report.Artifact{evidenceArtifact()},
		outcome:   report.RunOutcome{Status: "succeeded", ExitCode: 0},
	}

	// When
	verification, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.True(t, verification.Verified)
	assert.Contains(t, verification.Reason, "exited zero")
}

func Test_MarkVerified_does_not_set_verified_when_verification_fails(t *testing.T) {
	// Given: evidence exists but the run failed, so the finding stays unverified.
	evidence := &stubEvidence{
		artifacts: []report.Artifact{evidenceArtifact()},
		outcome:   report.RunOutcome{Status: "failed", ExitCode: 1},
	}
	findings := &stubFindings{}

	// When
	verification, err := report.MarkVerified(
		context.Background(), evidence, findings,
		domain.NewULIDGenerator().New(), domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.False(t, verification.Verified)
	assert.Zero(t, findings.calls,
		"a failed run must never cause a verification write")
}

func Test_MarkVerified_sets_verified_when_verification_passes(t *testing.T) {
	// Given
	evidence := &stubEvidence{
		artifacts: []report.Artifact{evidenceArtifact()},
		outcome:   report.RunOutcome{Status: "succeeded", ExitCode: 0},
	}
	findings := &stubFindings{}

	// When
	verification, err := report.MarkVerified(
		context.Background(), evidence, findings,
		domain.NewULIDGenerator().New(), domain.NewULIDGenerator().New())

	// Then
	require.NoError(t, err)
	assert.True(t, verification.Verified)
	assert.Equal(t, 1, findings.calls)
	assert.Equal(t, []bool{true}, findings.marked)
}

func Test_VerifyFinding_returns_an_error_when_the_store_is_nil(t *testing.T) {
	// When
	_, err := report.VerifyFinding(context.Background(), nil, domain.NewULIDGenerator().New())

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, report.ErrNoEvidence)
}

func Test_VerifyFinding_propagates_a_read_error_when_listing_fails(t *testing.T) {
	// Given
	sentinel := errors.New("database unavailable")
	evidence := &stubEvidence{listErr: sentinel}

	// When
	_, err := report.VerifyFinding(context.Background(), evidence, domain.NewULIDGenerator().New())

	// Then
	require.ErrorIs(t, err, sentinel)
}
