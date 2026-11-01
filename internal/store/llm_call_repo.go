package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/llm"
)

// ErrInvalidCallRecord is returned when a call record cannot be persisted.
var ErrInvalidCallRecord = errors.New("store: invalid llm call record")

// LLMCallRepo persists one row per provider request.
type LLMCallRepo struct {
	db *DB
}

// NewLLMCallRepo returns a repository over the shared database handle.
func NewLLMCallRepo(db *DB) *LLMCallRepo { return &LLMCallRepo{db: db} }

// Record persists a completed call attempt.
//
// The table is append-only and enforced by triggers, so this is the only write
// path. Both the request and the response are stored after redaction by the
// caller: this layer never inspects or trims their content.
func (r *LLMCallRepo) Record(ctx context.Context, rec llm.CallRecord, requestJSON, responseJSON string) error {
	if err := validateCallRecord(rec); err != nil {
		return err
	}

	const query = `
		INSERT INTO llm_calls (
			id, engagement_id, session_id, message_id, stage,
			provider, model_requested, model_served, fallback_index, purpose,
			request_json, response_json, status, http_status,
			error_code, error_message,
			tokens_in, tokens_out, tokens_cached, cost_usd_micros,
			latency_ms, streamed, reasoning_tokens, started_at, finished_at
		) VALUES (?,?,?,?,?, ?,?,?,?,?, ?,?,?,?, ?,?, ?,?,?,?, ?,?,?,?,?)`

	_, err := r.db.Writer().ExecContext(ctx, query,
		rec.ID.String(),
		rec.EngagementID.String(),
		nullID(rec.SessionID),
		nullID(rec.MessageID),
		string(rec.Stage),
		string(rec.Provider),
		rec.ModelRequested,
		nullString(rec.ModelServed),
		rec.FallbackIndex,
		string(rec.Purpose),
		requestJSON,
		nullString(responseJSON),
		rec.Status,
		nullInt(rec.HTTPStatus),
		nullString(rec.ErrorCode),
		nullString(rec.ErrorMessage),
		rec.Usage.InputTokens,
		rec.Usage.OutputTokens,
		rec.Usage.CachedTokens,
		rec.CostUSDMicros,
		rec.Latency.Milliseconds(),
		boolToInt(rec.Streamed),
		rec.Usage.ReasoningTokens,
		int64(rec.StartedAt),
		int64(rec.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("record llm call %s: %w", rec.ID, err)
	}
	return nil
}

// SumByStage returns token and cost totals for one stage of an engagement.
func (r *LLMCallRepo) SumByStage(ctx context.Context, engagementID domain.ID, stage domain.StageKind) (TokensIn, TokensOut int, CostMicros int64, err error) {
	const query = `
		SELECT COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0), COALESCE(SUM(cost_usd_micros),0)
		FROM llm_calls
		WHERE engagement_id = ? AND stage = ?`

	row := r.db.Reader().QueryRowContext(ctx, query, engagementID.String(), string(stage))
	if scanErr := row.Scan(&TokensIn, &TokensOut, &CostMicros); scanErr != nil {
		return 0, 0, 0, fmt.Errorf("sum llm calls for stage %s: %w", stage, scanErr)
	}
	return TokensIn, TokensOut, CostMicros, nil
}

// CountByEngagement returns how many calls were recorded for an engagement.
func (r *LLMCallRepo) CountByEngagement(ctx context.Context, engagementID domain.ID) (int, error) {
	const query = `SELECT COUNT(*) FROM llm_calls WHERE engagement_id = ?`

	var count int
	if err := r.db.Reader().QueryRowContext(ctx, query, engagementID.String()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count llm calls: %w", err)
	}
	return count, nil
}

// validateCallRecord rejects a record the schema would refuse anyway, so the
// failure names the offending field instead of surfacing a CHECK violation.
func validateCallRecord(rec llm.CallRecord) error {
	if rec.ID.IsZero() {
		return fmt.Errorf("%w: id is empty", ErrInvalidCallRecord)
	}
	if rec.EngagementID.IsZero() {
		return fmt.Errorf("%w: engagement_id is empty", ErrInvalidCallRecord)
	}
	if !rec.Stage.Valid() {
		return fmt.Errorf("%w: stage %q", ErrInvalidCallRecord, rec.Stage)
	}
	if !rec.Provider.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalidCallRecord, rec.Provider)
	}
	if !llm.Purpose(rec.Purpose).Valid() {
		return fmt.Errorf("%w: purpose %q", ErrInvalidCallRecord, rec.Purpose)
	}
	switch rec.Status {
	case "ok", "error", "timeout", "rate_limited", "cancelled":
	default:
		return fmt.Errorf("%w: status %q", ErrInvalidCallRecord, rec.Status)
	}
	if rec.StartedAt.IsZero() || rec.FinishedAt.IsZero() {
		return fmt.Errorf("%w: timestamps must be set", ErrInvalidCallRecord)
	}
	if rec.FinishedAt < rec.StartedAt {
		return fmt.Errorf("%w: finished_at precedes started_at", ErrInvalidCallRecord)
	}
	if rec.Usage.InputTokens < 0 || rec.Usage.OutputTokens < 0 || rec.Usage.CachedTokens < 0 {
		return fmt.Errorf("%w: token counts must not be negative", ErrInvalidCallRecord)
	}
	if rec.CostUSDMicros < 0 {
		return fmt.Errorf("%w: cost must not be negative", ErrInvalidCallRecord)
	}
	return nil
}

// nullInt converts a zero int into a SQL NULL.
func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// boolToInt converts a bool into the 0/1 the schema stores.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
