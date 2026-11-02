package approval_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/approval"
	"github.com/cosmics/cosmics/internal/domain"
)

// refNow is a fixed instant so every window in a test is explicit.
const refNow = domain.TimestampMs(1_760_000_000_000)

// validRequest returns a request that satisfies Validate.
func validRequest() approval.Request {
	return approval.Request{
		ID:             domain.NewULIDGenerator().New(),
		Kind:           approval.KindExploitLaunch,
		CommandPreview: "nmap -sV -p 443 example.test",
		Reason:         "confirm the service version before exploiting",
		Decision:       approval.DecisionPending,
		CreatedAt:      refNow,
		ExpiresAt:      refNow + 300_000,
	}
}

func Test_Request_Check_returns_nil_only_when_approved_and_unexpired(t *testing.T) {
	tests := []struct {
		name     string
		decision approval.Decision
		now      domain.TimestampMs
		want     error
	}{
		{"approved and live", approval.DecisionApproved, refNow + 1, nil},
		{"approved but past expiry", approval.DecisionApproved, refNow + 400_000, approval.ErrExpired},
		{"approved exactly at expiry", approval.DecisionApproved, refNow + 300_000, approval.ErrExpired},
		{"pending", approval.DecisionPending, refNow + 1, approval.ErrPending},
		{"denied", approval.DecisionDenied, refNow + 1, approval.ErrDenied},
		{"explicitly expired", approval.DecisionExpired, refNow + 1, approval.ErrExpired},
		{"unreadable decision", approval.Decision("nonsense"), refNow + 1, approval.ErrNotApproved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			req := validRequest()
			req.Decision = tt.decision

			// When
			err := req.Check(tt.now)

			// Then
			if tt.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func Test_Request_Validate_rejects_an_empty_command_preview_when_missing(t *testing.T) {
	// Given
	req := validRequest()
	req.CommandPreview = "   "

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, approval.ErrInvalidRequest)
	assert.Contains(t, err.Error(), "command_preview")
}

func Test_Request_Validate_rejects_an_empty_reason_when_missing(t *testing.T) {
	// Given
	req := validRequest()
	req.Reason = ""

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, approval.ErrInvalidRequest)
	assert.Contains(t, err.Error(), "reason")
}

func Test_Request_Validate_rejects_an_unknown_kind_when_given_one(t *testing.T) {
	// Given
	req := validRequest()
	req.Kind = approval.Kind("escalation")

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, approval.ErrInvalidRequest)
	assert.Contains(t, err.Error(), "kind")
}

func Test_Request_Validate_rejects_an_expiry_before_creation_when_malformed(t *testing.T) {
	// Given
	req := validRequest()
	req.ExpiresAt = refNow - 1

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, approval.ErrInvalidRequest)
	assert.Contains(t, err.Error(), "expires_at")
}

func Test_Request_Validate_rejects_an_expiry_equal_to_creation_when_zero_window(t *testing.T) {
	// Given
	req := validRequest()
	req.ExpiresAt = req.CreatedAt

	// When
	err := req.Validate()

	// Then
	require.ErrorIs(t, err, approval.ErrInvalidRequest)
}

func Test_Request_Validate_accepts_a_well_formed_request_when_checked(t *testing.T) {
	// Given
	req := validRequest()

	// When
	err := req.Validate()

	// Then
	require.NoError(t, err)
}

func Test_Request_IsTerminal_is_true_only_for_decided_states(t *testing.T) {
	tests := []struct {
		name     string
		decision approval.Decision
		now      domain.TimestampMs
		want     bool
	}{
		{"approved", approval.DecisionApproved, refNow + 1, true},
		{"denied", approval.DecisionDenied, refNow + 1, true},
		{"expired decision", approval.DecisionExpired, refNow + 1, true},
		{"pending but live", approval.DecisionPending, refNow + 1, false},
		{"pending but past deadline", approval.DecisionPending, refNow + 400_000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			req := validRequest()
			req.Decision = tt.decision

			// When
			got := req.IsTerminal(tt.now)

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Decision_Terminal_is_false_only_for_pending(t *testing.T) {
	for _, decision := range approval.AllDecisions {
		t.Run(string(decision), func(t *testing.T) {
			// When
			got := decision.Terminal()

			// Then
			assert.Equal(t, decision != approval.DecisionPending, got)
		})
	}
}

func Test_Kind_Valid_accepts_every_declared_kind_and_rejects_others(t *testing.T) {
	for _, kind := range approval.AllKinds {
		t.Run(string(kind), func(t *testing.T) {
			assert.True(t, kind.Valid())
		})
	}

	assert.False(t, approval.Kind("").Valid())
	assert.False(t, approval.Kind("other").Valid())
}

func Test_AllKinds_matches_the_database_check_list_when_compared(t *testing.T) {
	// Given: the approvals.kind CHECK constraint in 0001_init.sql names exactly
	// these four values. A mismatch would let the Go layer accept a row the
	// database rejects, which surfaces as an opaque constraint error at runtime.
	want := map[approval.Kind]bool{
		"destructive_action": true,
		"exploit_launch":     true,
		"credential_use":     true,
		"data_access":        true,
	}

	// When / Then
	require.Len(t, approval.AllKinds, len(want))
	for _, kind := range approval.AllKinds {
		assert.True(t, want[kind], "kind %q is not in the DDL CHECK list", kind)
	}
}
