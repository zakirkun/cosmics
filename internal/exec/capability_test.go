package exec_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/exec"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/tools"
)

// authClock returns a fixed instant.
type authClock struct{}

func (authClock) Now() domain.TimestampMs { return 1_760_000_000_000 }

// stubApprovals serves configured approvals and counts lookups.
type stubApprovals struct {
	rows    map[string]exec.ApprovalRow
	lookups int
	err     error
}

func newStubApprovals() *stubApprovals {
	return &stubApprovals{rows: make(map[string]exec.ApprovalRow)}
}

func (s *stubApprovals) GetApproval(_ context.Context, id domain.ID) (exec.ApprovalRow, error) {
	s.lookups++
	if s.err != nil {
		return exec.ApprovalRow{}, s.err
	}
	row, ok := s.rows[id.String()]
	if !ok {
		return exec.ApprovalRow{}, errors.New("not found")
	}
	return row, nil
}

// stubRejections records rejections it was asked to persist.
type stubRejections struct {
	rows    []store.ToolRunRow
	reasons []string
}

func (s *stubRejections) RecordRejection(_ context.Context, run store.ToolRunRow, reason string) (domain.ID, error) {
	s.rows = append(s.rows, run)
	s.reasons = append(s.reasons, reason)
	return run.ID, nil
}

// destructiveTool returns a destructive tool definition.
func destructiveTool() tools.Tool {
	return tools.Tool{
		ID:               "sqlmap",
		Category:         tools.CategoryExploit,
		DangerLevel:      tools.DangerDestructive,
		RequiresApproval: true,
		Enabled:          true,
	}
}

// safeTool returns a tool that needs no approval.
func safeTool() tools.Tool {
	return tools.Tool{
		ID:          "nmap",
		Category:    tools.CategoryNetwork,
		DangerLevel: tools.DangerSafe,
		Enabled:     true,
	}
}

// approvedRun builds an approval matching the spec produced by liveSpec.
func approvedRun(engagementID domain.ID, preview string) exec.ApprovalRow {
	return exec.ApprovalRow{
		ID:             domain.NewULIDGenerator().New(),
		EngagementID:   engagementID,
		CommandPreview: preview,
		Decision:       "approved",
		ExpiresAt:      1_760_000_000_000 + 300_000,
	}
}

// liveSpec returns a spec whose preview is known.
func liveSpec() exec.RunSpec {
	return exec.RunSpec{
		Image: "cosmics/kali-tools:latest",
		Argv:  []string{"sqlmap", "-u", "https://portal.acme.test", "--batch"},
	}
}

// newAuthorizer builds an authorizer over the stubs.
func newAuthorizer(approvals exec.ApprovalLookup, rejections exec.RejectionRecorder) *exec.Authorizer {
	authorizer, err := exec.NewAuthorizer(exec.AuthorizerOptions{
		Approvals:  approvals,
		Rejections: rejections,
		Clock:      authClock{},
	})
	if err != nil {
		panic(err)
	}
	return authorizer
}

func Test_Authorize_allows_a_safe_tool_without_an_approval(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), safeTool(), domain.NewULIDGenerator().New(), liveSpec(), domain.ID{})

	// Then
	require.NoError(t, err)
	assert.Zero(t, approvals.lookups, "a safe tool must not consult the approval table")
}

func Test_Authorize_rejects_a_destructive_tool_when_no_approval_id_is_supplied(t *testing.T) {
	// Given
	authorizer := newAuthorizer(newStubApprovals(), &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), domain.NewULIDGenerator().New(), liveSpec(), domain.ID{})

	// Then
	require.ErrorIs(t, err, exec.ErrNotApproved,
		"a destructive tool with no approval must never be authorized")
}

func Test_Authorize_rejects_a_destructive_tool_when_the_approval_is_pending(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	spec := liveSpec()
	engagementID := domain.NewULIDGenerator().New()

	approval := approvedRun(engagementID, "sqlmap -u https://portal.acme.test --batch")
	approval.Decision = "pending"
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
	assert.Contains(t, err.Error(), "pending")
}

func Test_Authorize_rejects_a_destructive_tool_when_the_approval_expired(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	spec := liveSpec()
	engagementID := domain.NewULIDGenerator().New()

	approval := approvedRun(engagementID, "sqlmap -u https://portal.acme.test --batch")
	approval.ExpiresAt = authClock{}.Now() - 1
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
	assert.Contains(t, err.Error(), "expired")
}

func Test_Authorize_allows_a_destructive_tool_when_the_approval_is_approved_and_unexpired(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	spec := liveSpec()
	engagementID := domain.NewULIDGenerator().New()

	approval := approvedRun(engagementID, "sqlmap -u https://portal.acme.test --batch")
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.NoError(t, err)
}

func Test_Authorize_rejects_an_approval_that_authorizes_a_different_command(t *testing.T) {
	// Given: the operator approved a different argv, so the approval must not
	// carry over to this one.
	approvals := newStubApprovals()
	spec := liveSpec()
	engagementID := domain.NewULIDGenerator().New()

	approval := approvedRun(engagementID, "sqlmap -u https://other.example --batch --risk=3")
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
	assert.Contains(t, err.Error(), "authorizes")
}

func Test_Authorize_rejects_an_approval_from_a_different_engagement(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	spec := liveSpec()

	issuerEngagement := domain.NewULIDGenerator().New()
	requestedEngagement := domain.NewULIDGenerator().New()

	approval := approvedRun(issuerEngagement, "sqlmap -u https://portal.acme.test --batch")
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), requestedEngagement, spec, approval.ID)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
	assert.Contains(t, err.Error(), "belongs to engagement")
}

func Test_Authorize_rejects_a_denied_approval(t *testing.T) {
	// Given
	approvals := newStubApprovals()
	spec := liveSpec()
	engagementID := domain.NewULIDGenerator().New()

	approval := approvedRun(engagementID, "sqlmap -u https://portal.acme.test --batch")
	approval.Decision = "denied"
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
	assert.Contains(t, err.Error(), "denied")
}

func Test_RecordRejection_writes_status_rejected_when_called(t *testing.T) {
	// Given
	rejections := &stubRejections{}
	authorizer := newAuthorizer(newStubApprovals(), rejections)

	row := store.ToolRunRow{
		ID:           domain.NewULIDGenerator().New(),
		EngagementID: domain.NewULIDGenerator().New(),
		ToolID:       "sqlmap",
		ArgvJSON:     `["sqlmap","-u","https://portal.acme.test"]`,
		ArgvDisplay:  "sqlmap -u https://portal.acme.test",
		Image:        "cosmics/kali-tools:latest",
		NetworkMode:  "bridge",
		Status:       store.RunQueued,
		StartedAt:    1_760_000_000_000,
	}

	// When
	id, err := authorizer.RecordRejection(context.Background(), row, "operator denied the action")

	// Then
	require.NoError(t, err)
	assert.Equal(t, row.ID, id)

	require.Len(t, rejections.rows, 1)
	assert.Empty(t, rejections.rows[0].ContainerID,
		"a rejected run never started, so it has no container")
	assert.Equal(t, "operator denied the action", rejections.reasons[0])
}

func Test_NewAuthorizer_rejects_a_missing_approvals_lookup_when_built(t *testing.T) {
	// Given
	opts := exec.AuthorizerOptions{Clock: authClock{}}

	// When
	_, err := exec.NewAuthorizer(opts)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
}

func Test_NewAuthorizer_rejects_a_missing_clock_when_built(t *testing.T) {
	// Given
	opts := exec.AuthorizerOptions{Approvals: newStubApprovals()}

	// When
	_, err := exec.NewAuthorizer(opts)

	// Then
	require.ErrorIs(t, err, exec.ErrApprovalInvalid)
}

func Test_Authorizer_quotes_a_token_with_a_space_when_rendering_the_preview(t *testing.T) {
	// Given: an argv element containing a space must render quoted, so the
	// operator reads a preview that unambiguously names one argument.
	approvals := newStubApprovals()
	engagementID := domain.NewULIDGenerator().New()

	spec := exec.RunSpec{
		Image: "cosmics/kali-tools:latest",
		Argv:  []string{"tool", "two words"},
	}

	approval := approvedRun(engagementID, "tool 'two words'")
	approvals.rows[approval.ID.String()] = approval

	authorizer := newAuthorizer(approvals, &stubRejections{})

	// When
	err := authorizer.Authorize(context.Background(), destructiveTool(), engagementID, spec, approval.ID)

	// Then
	require.NoError(t, err)
}
