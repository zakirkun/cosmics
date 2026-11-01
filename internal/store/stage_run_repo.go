package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrInvalidStageRun is returned when a stage run cannot be persisted.
var ErrInvalidStageRun = errors.New("store: invalid stage run")

// ErrNoStageRun is returned when an engagement has no run for a stage.
var ErrNoStageRun = errors.New("store: stage run not found")

// StageRun is one attempt at one pipeline stage.
type StageRun struct {
	ID            domain.ID
	EngagementID  domain.ID
	Stage         domain.StageKind
	Attempt       int
	Status        domain.StageRunStatus
	SessionID     domain.ID
	PlanJSON      string
	Summary       string
	StartedAt     domain.TimestampMs
	FinishedAt    domain.TimestampMs
	ToolRunCount  int
	FindingCount  int
	FactCount     int
	TokensIn      int
	TokensOut     int
	CostUSDMicros int64
	Iterations    int
	Err           string
}

// StageRunRepo persists pipeline stage attempts.
type StageRunRepo struct {
	db *DB
}

// NewStageRunRepo returns a repository over the shared database handle.
func NewStageRunRepo(db *DB) *StageRunRepo { return &StageRunRepo{db: db} }

// Begin opens a new attempt for a stage, computing the next attempt number.
//
// The attempt number is derived inside the same transaction that inserts the
// row, so two concurrent callers cannot both claim attempt 1.
func (r *StageRunRepo) Begin(ctx context.Context, engagementID domain.ID, stage domain.StageKind, at domain.TimestampMs, newID domain.ID) (StageRun, error) {
	if engagementID.IsZero() || newID.IsZero() {
		return StageRun{}, fmt.Errorf("%w: identifiers must be set", ErrInvalidStageRun)
	}
	if !stage.Valid() {
		return StageRun{}, fmt.Errorf("%w: stage %q", ErrInvalidStageRun, stage)
	}

	run := StageRun{
		ID:           newID,
		EngagementID: engagementID,
		Stage:        stage,
		Status:       domain.StageRunning,
		StartedAt:    at,
	}

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		const nextAttempt = `
			SELECT COALESCE(MAX(attempt), 0) + 1
			FROM stage_runs WHERE engagement_id = ? AND stage = ?`

		if scanErr := tx.QueryRowContext(ctx, nextAttempt, engagementID.String(), string(stage)).Scan(&run.Attempt); scanErr != nil {
			return fmt.Errorf("resolve attempt number: %w", scanErr)
		}

		const insert = `
			INSERT INTO stage_runs (id, engagement_id, stage, attempt, status, started_at)
			VALUES (?,?,?,?,?,?)`

		if _, execErr := tx.ExecContext(ctx, insert,
			run.ID.String(), run.EngagementID.String(), string(run.Stage),
			run.Attempt, string(run.Status), int64(run.StartedAt),
		); execErr != nil {
			return fmt.Errorf("insert stage run: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return StageRun{}, fmt.Errorf("begin stage run for %s: %w", stage, err)
	}
	return run, nil
}

// Finish closes a run with its terminal status and rollups.
func (r *StageRunRepo) Finish(ctx context.Context, run StageRun, at domain.TimestampMs) error {
	if run.ID.IsZero() {
		return fmt.Errorf("%w: id is empty", ErrInvalidStageRun)
	}
	if !run.Status.Terminal() {
		return fmt.Errorf("%w: status %q is not terminal", ErrInvalidStageRun, run.Status)
	}

	const query = `
		UPDATE stage_runs SET
			status = ?, summary = ?, plan_json = ?, session_id = ?,
			finished_at = ?, tool_run_count = ?, finding_count = ?, fact_count = ?,
			tokens_in = ?, tokens_out = ?, cost_usd_micros = ?, iterations = ?, error = ?
		WHERE id = ?`

	result, err := r.db.Writer().ExecContext(ctx, query,
		string(run.Status),
		nullString(run.Summary),
		nullString(run.PlanJSON),
		nullID(run.SessionID),
		int64(at),
		run.ToolRunCount,
		run.FindingCount,
		run.FactCount,
		run.TokensIn,
		run.TokensOut,
		run.CostUSDMicros,
		run.Iterations,
		nullString(run.Err),
		run.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("finish stage run %s: %w", run.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish stage run %s: %w", run.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("stage run %s: %w", run.ID, ErrNotFound)
	}
	return nil
}

// Latest returns the most recent attempt for a stage.
func (r *StageRunRepo) Latest(ctx context.Context, engagementID domain.ID, stage domain.StageKind) (StageRun, error) {
	const query = `
		SELECT id, engagement_id, stage, attempt, status, session_id,
		       plan_json, summary, started_at, finished_at,
		       tool_run_count, finding_count, fact_count,
		       tokens_in, tokens_out, cost_usd_micros, iterations, error
		FROM stage_runs
		WHERE engagement_id = ? AND stage = ?
		ORDER BY attempt DESC LIMIT 1`

	row := r.db.Reader().QueryRowContext(ctx, query, engagementID.String(), string(stage))
	run, err := scanStageRun(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return StageRun{}, fmt.Errorf("stage %s: %w", stage, ErrNoStageRun)
		}
		return StageRun{}, fmt.Errorf("latest stage run for %s: %w", stage, err)
	}
	return run, nil
}

// List returns every attempt for an engagement, oldest first.
func (r *StageRunRepo) List(ctx context.Context, engagementID domain.ID) ([]StageRun, error) {
	const query = `
		SELECT id, engagement_id, stage, attempt, status, session_id,
		       plan_json, summary, started_at, finished_at,
		       tool_run_count, finding_count, fact_count,
		       tokens_in, tokens_out, cost_usd_micros, iterations, error
		FROM stage_runs
		WHERE engagement_id = ?
		ORDER BY started_at ASC, attempt ASC`

	rows, err := r.db.Reader().QueryContext(ctx, query, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list stage runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var runs []StageRun
	for rows.Next() {
		run, scanErr := scanStageRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stage runs: %w", err)
	}
	return runs, nil
}

// MarkInterrupted closes any run left in a non-terminal state by a crash.
//
// Startup calls this so a killed process cannot leave a stage pinned as running
// and block every subsequent attempt.
func (r *StageRunRepo) MarkInterrupted(ctx context.Context, at domain.TimestampMs, cause string) (int, error) {
	const query = `
		UPDATE stage_runs
		SET status = ?, finished_at = ?, error = ?
		WHERE status IN (?, ?)`

	result, err := r.db.Writer().ExecContext(ctx, query,
		string(domain.StageFailed), int64(at), cause,
		string(domain.StagePending), string(domain.StageRunning),
	)
	if err != nil {
		return 0, fmt.Errorf("mark interrupted stage runs: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mark interrupted stage runs: %w", err)
	}
	return int(affected), nil
}

// scanStageRun maps one row of the standard stage_runs projection.
func scanStageRun(scanner rowScanner) (StageRun, error) {
	var (
		run        StageRun
		id         string
		engID      string
		stage      string
		status     string
		sessionID  *string
		planJSON   *string
		summary    *string
		startedAt  *int64
		finishedAt *int64
		errText    *string
	)

	if err := scanner.Scan(
		&id, &engID, &stage, &run.Attempt, &status, &sessionID,
		&planJSON, &summary, &startedAt, &finishedAt,
		&run.ToolRunCount, &run.FindingCount, &run.FactCount,
		&run.TokensIn, &run.TokensOut, &run.CostUSDMicros, &run.Iterations, &errText,
	); err != nil {
		if isNoRows(err) {
			return StageRun{}, ErrNotFound
		}
		return StageRun{}, fmt.Errorf("scan stage run: %w", err)
	}

	parsedID, err := domain.NewID(id)
	if err != nil {
		return StageRun{}, fmt.Errorf("scan stage run id: %w", err)
	}
	parsedEngID, err := domain.NewID(engID)
	if err != nil {
		return StageRun{}, fmt.Errorf("scan stage run engagement id: %w", err)
	}

	run.ID = parsedID
	run.EngagementID = parsedEngID
	run.Stage = domain.StageKind(stage)
	run.Status = domain.StageRunStatus(status)
	run.PlanJSON = derefString(planJSON)
	run.Summary = derefString(summary)
	run.Err = derefString(errText)
	run.StartedAt = derefTimestamp(startedAt)
	run.FinishedAt = derefTimestamp(finishedAt)

	if sessionID != nil {
		if parsed, sessionErr := domain.NewID(*sessionID); sessionErr == nil {
			run.SessionID = parsed
		}
	}
	return run, nil
}

// derefString returns the pointed-to string or the empty value.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefTimestamp returns the pointed-to instant or the zero value.
func derefTimestamp(p *int64) domain.TimestampMs {
	if p == nil {
		return 0
	}
	return domain.TimestampMs(*p)
}
