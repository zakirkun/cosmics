package store

import (
	"database/sql"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// rowScanner is satisfied by both *sql.Row and *sql.Rows, which lets one scanner
// serve Get (single row) and List (many rows).
type rowScanner interface {
	Scan(dest ...any) error
}

// engagementColumns is the projection every engagement read uses. The order is
// the order scanEngagement expects.
const engagementColumns = `id, name, client, mode, status, stage, model_profile,
	authorized_by, authorization_note, authorized_at,
	budget_usd_micros, spent_usd_micros, token_budget, tokens_used,
	created_at, updated_at, started_at, finished_at, config_json, error`

// scanEngagement maps one row of engagementColumns into a domain value.
//
// Nullable columns collapse to the domain's zero value: NULL client is "",
// NULL started_at is the zero TimestampMs, and so on. The domain type has no
// way to express "explicitly null", and every consumer treats the zero value
// as "not set".
func scanEngagement(scanner rowScanner) (domain.Engagement, error) {
	var (
		engagement                   domain.Engagement
		id, name, mode, status       string
		stage, profile, authorizedBy string
		client, authNote, errText    sql.NullString
		authorizedAt                 int64
		budget, spent, tokens        int64
		tokensUsed                   int64
		createdAt, updatedAt         int64
		startedAt, finishedAt        sql.NullInt64
		configJSON                   string
	)

	err := scanner.Scan(
		&id, &name, &client, &mode, &status, &stage, &profile,
		&authorizedBy, &authNote, &authorizedAt,
		&budget, &spent, &tokens, &tokensUsed,
		&createdAt, &updatedAt, &startedAt, &finishedAt, &configJSON, &errText,
	)
	if err != nil {
		return domain.Engagement{}, err
	}

	parsedID, err := domain.NewID(id)
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("engagement %q id: %w", id, err)
	}
	parsedStatus, err := parseStatus(status)
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("engagement %q: %w", id, err)
	}
	parsedStage, err := parseEngageStage(stage)
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("engagement %q: %w", id, err)
	}

	engagement.ID = parsedID
	engagement.Name = name
	engagement.Client = client.String
	engagement.Mode = domain.Mode(mode)
	engagement.Status = parsedStatus
	engagement.Stage = parsedStage
	engagement.Profile = profile
	engagement.Authorization = domain.Authorization{
		AuthorizedBy: authorizedBy,
		Note:         authNote.String,
		AuthorizedAt: domain.TimestampMs(authorizedAt),
	}
	engagement.BudgetUSDMicros = budget
	engagement.SpentUSDMicros = spent
	engagement.TokenBudget = tokens
	engagement.TokensUsed = tokensUsed
	engagement.CreatedAt = domain.TimestampMs(createdAt)
	engagement.UpdatedAt = domain.TimestampMs(updatedAt)
	engagement.StartedAt = domain.TimestampMs(startedAt.Int64)
	engagement.FinishedAt = domain.TimestampMs(finishedAt.Int64)
	engagement.ConfigJSON = configJSON
	engagement.Err = errText.String

	return engagement, nil
}

// parseStatus validates a stored status string through the domain's own check,
// so an unknown value surfaces as domain.ErrUnknownStatus rather than a silent
// zero Status.
func parseStatus(raw string) (domain.Status, error) {
	status := domain.Status(raw)
	if !status.Valid() {
		return "", fmt.Errorf("stored status %q: %w", raw, domain.ErrUnknownStatus)
	}
	return status, nil
}

// parseEngageStage validates a stored pipeline pointer.
func parseEngageStage(raw string) (domain.EngageStage, error) {
	stage := domain.EngageStage(raw)
	if !stage.Valid() {
		return "", fmt.Errorf("stored stage %q: %w", raw, domain.ErrUnknownStage)
	}
	return stage, nil
}
