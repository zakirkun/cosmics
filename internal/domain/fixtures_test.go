package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

// allowedStatusMoves is an independent restatement of docs/CORE.md §12.1.
// It deliberately does not reuse any package-private map.
var allowedStatusMoves = map[domain.Status][]domain.Status{
	domain.StatusDraft:     {domain.StatusReady},
	domain.StatusReady:     {domain.StatusRunning, domain.StatusAborted},
	domain.StatusRunning:   {domain.StatusPaused, domain.StatusCompleted, domain.StatusFailed, domain.StatusAborted},
	domain.StatusPaused:    {domain.StatusRunning, domain.StatusAborted},
	domain.StatusCompleted: {domain.StatusReady},
	domain.StatusFailed:    {domain.StatusReady},
	domain.StatusAborted:   {domain.StatusReady},
}

func newValidTarget(t *testing.T) domain.Target {
	t.Helper()

	target, err := domain.NewTarget(domain.TargetDomain, "example.com")
	require.NoError(t, err)
	return target
}

func validEngagementParams(t *testing.T) domain.NewEngagementParams {
	t.Helper()

	return domain.NewEngagementParams{
		Name:    "Acme External Assessment",
		Client:  "Acme",
		Mode:    domain.ModeFull,
		Profile: "default",
		Authorization: domain.Authorization{
			AuthorizedBy: "CISO",
			Note:         "SOW-42",
			AuthorizedAt: refTime,
		},
		Targets:      []domain.Target{newValidTarget(t)},
		BudgetMicros: 5_000_000,
		TokenBudget:  250_000,
		Now:          refTime,
	}
}

func draftEngagement(t *testing.T) domain.Engagement {
	t.Helper()

	e, err := domain.NewEngagement(validEngagementParams(t))
	require.NoError(t, err)
	require.Equal(t, domain.StatusDraft, e.Status)
	return e
}

func readyEngagement(t *testing.T) domain.Engagement {
	t.Helper()

	e, err := draftEngagement(t).Authorize(t1)
	require.NoError(t, err)
	require.Equal(t, domain.StatusReady, e.Status)
	return e
}

func runningEngagement(t *testing.T) domain.Engagement {
	t.Helper()

	e, err := readyEngagement(t).Start(t2)
	require.NoError(t, err)
	require.Equal(t, domain.StatusRunning, e.Status)
	return e
}
