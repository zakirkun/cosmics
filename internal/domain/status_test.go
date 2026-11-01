package domain_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Status_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{name: "draft", status: domain.StatusDraft, want: true},
		{name: "ready", status: domain.StatusReady, want: true},
		{name: "running", status: domain.StatusRunning, want: true},
		{name: "paused", status: domain.StatusPaused, want: true},
		{name: "completed", status: domain.StatusCompleted, want: true},
		{name: "failed", status: domain.StatusFailed, want: true},
		{name: "aborted", status: domain.StatusAborted, want: true},
		{name: "empty", status: domain.Status(""), want: false},
		{name: "unknown", status: domain.Status("bogus"), want: false},
		{name: "stage value", status: domain.Status("recon"), want: false},
		{name: "wrong case", status: domain.Status("Draft"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			status := tt.status

			// When
			got := status.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Status_Terminal_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{name: "draft", status: domain.StatusDraft, want: false},
		{name: "ready", status: domain.StatusReady, want: false},
		{name: "running", status: domain.StatusRunning, want: false},
		{name: "paused", status: domain.StatusPaused, want: false},
		{name: "completed", status: domain.StatusCompleted, want: true},
		{name: "failed", status: domain.StatusFailed, want: true},
		{name: "aborted", status: domain.StatusAborted, want: true},
		{name: "empty", status: domain.Status(""), want: false},
		{name: "unknown", status: domain.Status("bogus"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			status := tt.status

			// When
			got := status.Terminal()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Status_Transition_ReturnsNil_when_MoveIsAllowed(t *testing.T) {
	for _, from := range domain.AllStatuses {
		for _, to := range allowedStatusMoves[from] {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				// Given
				// When
				err := from.Transition(to)

				// Then
				require.NoError(t, err)
				assert.True(t, from.CanTransitionTo(to))
			})
		}
	}
}

func Test_Status_Transition_ReturnsIllegalMove_when_MoveIsNotDeclared(t *testing.T) {
	tests := []struct {
		name string
		from domain.Status
		to   domain.Status
	}{
		{name: "draft to running", from: domain.StatusDraft, to: domain.StatusRunning},
		{name: "draft to paused", from: domain.StatusDraft, to: domain.StatusPaused},
		{name: "draft to completed", from: domain.StatusDraft, to: domain.StatusCompleted},
		{name: "draft to failed", from: domain.StatusDraft, to: domain.StatusFailed},
		{name: "draft to aborted", from: domain.StatusDraft, to: domain.StatusAborted},
		{name: "ready to completed", from: domain.StatusReady, to: domain.StatusCompleted},
		{name: "ready to failed", from: domain.StatusReady, to: domain.StatusFailed},
		{name: "ready to paused", from: domain.StatusReady, to: domain.StatusPaused},
		{name: "running to ready", from: domain.StatusRunning, to: domain.StatusReady},
		{name: "running to draft", from: domain.StatusRunning, to: domain.StatusDraft},
		{name: "paused to completed", from: domain.StatusPaused, to: domain.StatusCompleted},
		{name: "paused to failed", from: domain.StatusPaused, to: domain.StatusFailed},
		{name: "paused to draft", from: domain.StatusPaused, to: domain.StatusDraft},
		{name: "completed to running", from: domain.StatusCompleted, to: domain.StatusRunning},
		{name: "completed to failed", from: domain.StatusCompleted, to: domain.StatusFailed},
		{name: "completed to aborted", from: domain.StatusCompleted, to: domain.StatusAborted},
		{name: "failed to running", from: domain.StatusFailed, to: domain.StatusRunning},
		{name: "failed to completed", from: domain.StatusFailed, to: domain.StatusCompleted},
		{name: "failed to aborted", from: domain.StatusFailed, to: domain.StatusAborted},
		{name: "aborted to running", from: domain.StatusAborted, to: domain.StatusRunning},
		{name: "aborted to paused", from: domain.StatusAborted, to: domain.StatusPaused},
		{name: "aborted to completed", from: domain.StatusAborted, to: domain.StatusCompleted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			from, to := tt.from, tt.to

			// When
			err := from.Transition(to)

			// Then
			require.ErrorIs(t, err, domain.ErrIllegalMove)
			assert.False(t, from.CanTransitionTo(to))
		})
	}
}

func Test_Status_Transition_MatchesSpecifiedMatrix_when_EveryPairIsTried(t *testing.T) {
	for _, from := range domain.AllStatuses {
		for _, to := range domain.AllStatuses {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				// Given
				wantAllowed := slices.Contains(allowedStatusMoves[from], to)

				// When
				err := from.Transition(to)

				// Then
				if wantAllowed {
					require.NoError(t, err)
					assert.True(t, from.CanTransitionTo(to))
					return
				}

				require.ErrorIs(t, err, domain.ErrIllegalMove)
				assert.False(t, from.CanTransitionTo(to))
			})
		}
	}
}

func Test_Status_Transition_ReturnsUnknownStatus_when_SourceIsUndeclared(t *testing.T) {
	// Given
	unknown := domain.Status("bogus")

	// When
	err := unknown.Transition(domain.StatusReady)

	// Then
	require.ErrorIs(t, err, domain.ErrUnknownStatus)
	require.NotErrorIs(t, err, domain.ErrIllegalMove)
	require.False(t, unknown.CanTransitionTo(domain.StatusReady))
}

func Test_Status_Transition_ReturnsUnknownStatus_when_TargetIsUndeclared(t *testing.T) {
	// Given
	unknown := domain.Status("bogus")

	// When
	err := domain.StatusDraft.Transition(unknown)

	// Then
	require.ErrorIs(t, err, domain.ErrUnknownStatus)
	require.NotErrorIs(t, err, domain.ErrIllegalMove)
	require.False(t, domain.StatusDraft.CanTransitionTo(unknown))
}

func Test_Status_Transition_ReopensTerminalState_when_MovingToReady(t *testing.T) {
	terminals := []domain.Status{domain.StatusCompleted, domain.StatusFailed, domain.StatusAborted}

	for _, status := range terminals {
		t.Run(string(status), func(t *testing.T) {
			// Given
			require.True(t, status.Terminal())

			// When
			err := status.Transition(domain.StatusReady)

			// Then
			require.NoError(t, err)
		})
	}
}

func Test_AllStatuses_AreValid_when_Enumerated(t *testing.T) {
	// Given
	statuses := domain.AllStatuses

	// When & Then
	require.Len(t, statuses, 7)

	seen := make(map[domain.Status]struct{}, len(statuses))
	for _, status := range statuses {
		require.True(t, status.Valid(), "status %q must be valid", status)
		require.NotEmpty(t, string(status))

		_, duplicate := seen[status]
		require.False(t, duplicate, "status %q must appear once", status)
		seen[status] = struct{}{}
	}

	// Every declared status appears in the transition matrix, even if its only
	// outgoing move is the audited reopen.
	for _, status := range statuses {
		require.Contains(t, allowedStatusMoves, status)
	}
}

func Test_Authorization_Validate_ReturnsNil_when_ContractIsSatisfied(t *testing.T) {
	// Given
	auth := domain.Authorization{
		AuthorizedBy: "CISO",
		Note:         "SOW-42",
		AuthorizedAt: refTime,
	}

	// When
	err := auth.Validate()

	// Then
	require.NoError(t, err)
}

func Test_Authorization_Validate_ReturnsUnauthorized_when_AuthorizerIsBlank(t *testing.T) {
	tests := []struct {
		name string
		by   string
		at   domain.TimestampMs
	}{
		{name: "empty authorizer", by: "", at: refTime},
		{name: "space authorizer", by: " ", at: refTime},
		{name: "tab and newline authorizer", by: "\t\r\n", at: refTime},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			auth := domain.Authorization{AuthorizedBy: tt.by, AuthorizedAt: tt.at}

			// When
			err := auth.Validate()

			// Then
			assert.ErrorIs(t, err, domain.ErrUnauthorized)
		})
	}
}

func Test_Authorization_Validate_ReturnsUnauthorized_when_InstantIsZero(t *testing.T) {
	// Given
	auth := domain.Authorization{AuthorizedBy: "CISO", AuthorizedAt: 0}

	// When
	err := auth.Validate()

	// Then
	require.ErrorIs(t, err, domain.ErrUnauthorized)
}

func Test_ValidateName_AcceptsName_when_ValueIsWithinBounds(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "simple", input: "Acme External"},
		{name: "padded", input: "  Acme External  "},
		{name: "single character", input: "A"},
		{name: "exactly max", input: strings.Repeat("a", 200)},
		{name: "exactly max padded", input: "  " + strings.Repeat("a", 200) + "  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			err := domain.ValidateName(input)

			// Then
			require.NoError(t, err)
		})
	}
}

func Test_ValidateName_RejectsName_when_ValueIsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "empty", input: "", want: domain.ErrEmptyName},
		{name: "spaces", input: "   ", want: domain.ErrEmptyName},
		{name: "tabs and newlines", input: "\t\r\n", want: domain.ErrEmptyName},
		{name: "one over max", input: strings.Repeat("a", 201), want: domain.ErrNameTooLong},
		{name: "far over max", input: strings.Repeat("a", 10_000), want: domain.ErrNameTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.input

			// When
			err := domain.ValidateName(input)

			// Then
			assert.ErrorIs(t, err, tt.want)
		})
	}
}
