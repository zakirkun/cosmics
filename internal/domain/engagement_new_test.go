package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_NewEngagement_BuildsDraft_when_ParametersAreValid(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.Name = "  Acme External Assessment  "
	p.Client = "  Acme  "
	p.Profile = ""

	// When
	e, err := domain.NewEngagement(p)

	// Then
	require.NoError(t, err)

	require.Equal(t, "Acme External Assessment", e.Name)
	require.Equal(t, "Acme", e.Client)
	require.Equal(t, p.Mode, e.Mode)
	require.Equal(t, domain.StatusDraft, e.Status)
	require.Equal(t, domain.EngageStageNone, e.Stage)
	require.Equal(t, "default", e.Profile)
	require.Equal(t, p.Authorization, e.Authorization)
	require.Equal(t, p.BudgetMicros, e.BudgetUSDMicros)
	require.Equal(t, p.TokenBudget, e.TokenBudget)
	require.Equal(t, int64(0), e.SpentUSDMicros)
	require.Equal(t, int64(0), e.TokensUsed)
	require.Equal(t, p.Now, e.CreatedAt)
	require.Equal(t, p.Now, e.UpdatedAt)
	require.True(t, e.StartedAt.IsZero())
	require.True(t, e.FinishedAt.IsZero())
	require.Equal(t, "{}", e.ConfigJSON)
	require.Empty(t, e.Err)
	require.True(t, e.ID.IsZero())
	require.False(t, e.Status.Terminal())
}

func Test_NewEngagement_KeepsCustomProfile_when_ProfileIsProvided(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.Profile = "  high-context  "

	// When
	e, err := domain.NewEngagement(p)

	// Then
	require.NoError(t, err)
	require.Equal(t, "high-context", e.Profile)
}

func Test_NewEngagement_NormalizesProfileToDefault_when_ProfileIsWhitespace(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.Profile = " \t\r\n "

	// When
	e, err := domain.NewEngagement(p)

	// Then
	require.NoError(t, err)
	require.Equal(t, "default", e.Profile)
}

func Test_NewEngagement_AcceptsZeroBudgets_when_BudgetsMeanUnlimited(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.BudgetMicros = 0
	p.TokenBudget = 0

	// When
	e, err := domain.NewEngagement(p)

	// Then
	require.NoError(t, err)
	require.Equal(t, int64(0), e.BudgetUSDMicros)
	require.Equal(t, int64(0), e.TokenBudget)
}

func Test_NewEngagement_AcceptsNameAtLengthLimit_when_NameIsExactlyMax(t *testing.T) {
	// Given
	p := validEngagementParams(t)
	p.Name = strings.Repeat("n", 200)

	// When
	e, err := domain.NewEngagement(p)

	// Then
	require.NoError(t, err)
	require.Len(t, e.Name, 200)
}

func Test_NewEngagement_RejectsInput_when_ContractIsViolated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *domain.NewEngagementParams)
		want   error
	}{
		{
			name:   "empty name",
			mutate: func(p *domain.NewEngagementParams) { p.Name = "" },
			want:   domain.ErrEmptyName,
		},
		{
			name:   "blank name",
			mutate: func(p *domain.NewEngagementParams) { p.Name = "   \t " },
			want:   domain.ErrEmptyName,
		},
		{
			name:   "name too long",
			mutate: func(p *domain.NewEngagementParams) { p.Name = strings.Repeat("n", 201) },
			want:   domain.ErrNameTooLong,
		},
		{
			name:   "unknown mode",
			mutate: func(p *domain.NewEngagementParams) { p.Mode = domain.Mode("bogus") },
			want:   domain.ErrUnknownMode,
		},
		{
			name:   "empty mode",
			mutate: func(p *domain.NewEngagementParams) { p.Mode = domain.Mode("") },
			want:   domain.ErrUnknownMode,
		},
		{
			name:   "missing authorizer",
			mutate: func(p *domain.NewEngagementParams) { p.Authorization.AuthorizedBy = "" },
			want:   domain.ErrUnauthorized,
		},
		{
			name:   "blank authorizer",
			mutate: func(p *domain.NewEngagementParams) { p.Authorization.AuthorizedBy = "  " },
			want:   domain.ErrUnauthorized,
		},
		{
			name:   "missing authorization instant",
			mutate: func(p *domain.NewEngagementParams) { p.Authorization.AuthorizedAt = 0 },
			want:   domain.ErrUnauthorized,
		},
		{
			name:   "no targets",
			mutate: func(p *domain.NewEngagementParams) { p.Targets = nil },
			want:   domain.ErrNoTargets,
		},
		{
			name:   "empty target slice",
			mutate: func(p *domain.NewEngagementParams) { p.Targets = []domain.Target{} },
			want:   domain.ErrNoTargets,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEngagementParams(t)
			tt.mutate(&p)

			// When
			e, err := domain.NewEngagement(p)

			// Then
			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, domain.Engagement{}, e)
		})
	}
}

func Test_NewEngagement_RejectsBudgets_when_ValueIsNegative(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *domain.NewEngagementParams)
	}{
		{
			name:   "negative dollar budget",
			mutate: func(p *domain.NewEngagementParams) { p.BudgetMicros = -1 },
		},
		{
			name:   "negative token budget",
			mutate: func(p *domain.NewEngagementParams) { p.TokenBudget = -1 },
		},
		{
			name: "both negative",
			mutate: func(p *domain.NewEngagementParams) {
				p.BudgetMicros = -1
				p.TokenBudget = -1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEngagementParams(t)
			tt.mutate(&p)

			// When
			e, err := domain.NewEngagement(p)

			// Then
			require.Error(t, err)
			require.Equal(t, domain.Engagement{}, e)
			require.NotErrorIs(t, err, domain.ErrNoTargets)
		})
	}
}
