package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors for tool-run persistence.
var (
	ErrInvalidToolRun = errors.New("store: invalid tool run")
	ErrToolRunMissing = errors.New("store: tool run not found")
)

// ToolRunStatus is the lifecycle of one execution attempt.
type ToolRunStatus string

// Run statuses, matching the tool_runs.status CHECK constraint.
const (
	RunQueued    ToolRunStatus = "queued"
	RunRunning   ToolRunStatus = "running"
	RunSucceeded ToolRunStatus = "succeeded"
	RunFailed    ToolRunStatus = "failed"
	RunTimeout   ToolRunStatus = "timeout"
	RunCancelled ToolRunStatus = "cancelled"
	RunRejected  ToolRunStatus = "rejected"
)

// AllRunStatuses lists every declared status.
var AllRunStatuses = []ToolRunStatus{
	RunQueued, RunRunning, RunSucceeded, RunFailed, RunTimeout, RunCancelled, RunRejected,
}

// Valid reports whether the status is accepted by the schema.
func (s ToolRunStatus) Valid() bool {
	for _, declared := range AllRunStatuses {
		if declared == s {
			return true
		}
	}
	return false
}

// Terminal reports whether the status ends the attempt.
//
// A rejected run is terminal from the start: nothing was started, so nothing
// can finish.
func (s ToolRunStatus) Terminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunTimeout, RunCancelled, RunRejected:
		return true
	case RunQueued, RunRunning:
		return false
	}
	return false
}

// ToolRunRow is one tool_runs row.
type ToolRunRow struct {
	ID           domain.ID
	EngagementID domain.ID
	StageRunID   domain.ID
	LLMCallID    domain.ID
	ApprovalID   domain.ID

	ToolID      string
	ArgvJSON    string
	ArgvDisplay string
	Image       string
	ImageDigest string

	ContainerID string

	NetworkMode string
	CapsJSON    string
	Target      string

	Status   ToolRunStatus
	ExitCode int

	OutputPreview string
	OutputSHA256  string

	StartedAt  domain.TimestampMs
	FinishedAt domain.TimestampMs
	DurationMs int64

	ParsedJSON string
	Error      string
}

// Validate enforces the schema's constraints before a write.
func (r ToolRunRow) Validate() error {
	if r.EngagementID.IsZero() {
		return fmt.Errorf("engagement_id: %w", ErrInvalidToolRun)
	}
	if r.ToolID == "" {
		return fmt.Errorf("tool_id: %w", ErrInvalidToolRun)
	}
	if r.ArgvJSON == "" {
		return fmt.Errorf("argv_json: %w", ErrInvalidToolRun)
	}
	if r.ArgvDisplay == "" {
		return fmt.Errorf("argv_display: %w", ErrInvalidToolRun)
	}
	if r.Image == "" {
		return fmt.Errorf("image: %w", ErrInvalidToolRun)
	}
	if r.NetworkMode == "" {
		return fmt.Errorf("network_mode: %w", ErrInvalidToolRun)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("status %q: %w", r.Status, ErrInvalidToolRun)
	}
	if r.StartedAt.IsZero() {
		return fmt.Errorf("started_at: %w", ErrInvalidToolRun)
	}
	return nil
}

// ToolRunRepo persists execution attempts.
type ToolRunRepo struct {
	db *DB
}

// NewToolRunRepo returns a repository over the tool_runs table.
func NewToolRunRepo(db *DB) *ToolRunRepo {
	return &ToolRunRepo{db: db}
}

// toolRunColumns lists the columns every read selects, so the scan order cannot
// drift between the single-row and list queries.
const toolRunColumns = `id, engagement_id, stage_run_id, llm_call_id, approval_id,
	tool_id, argv_json, argv_display, image, image_digest, container_id,
	network_mode, caps_json, target, status, exit_code,
	output_preview, output_sha256, started_at, finished_at, duration_ms,
	parsed_json, error`

// Begin inserts a run in the running state.
//
// A run is inserted as running rather than queued: the executor has already been
// asked to start, so claiming "queued" would describe a state nothing observes.
func (r *ToolRunRepo) Begin(ctx context.Context, row ToolRunRow) (ToolRunRow, error) {
	row.Status = RunRunning
	row.FinishedAt = 0
	row.ExitCode = 0

	if err := row.Validate(); err != nil {
		return ToolRunRow{}, err
	}

	const query = `INSERT INTO tool_runs (` + toolRunColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?, NULL, NULL, NULL, NULL)
		RETURNING ` + toolRunColumns

	stored, err := scanToolRun(r.db.Writer().QueryRowContext(ctx, query,
		row.ID.String(),
		row.EngagementID.String(),
		nullID(row.StageRunID),
		nullID(row.LLMCallID),
		nullID(row.ApprovalID),
		row.ToolID,
		row.ArgvJSON,
		row.ArgvDisplay,
		row.Image,
		nullString(row.ImageDigest),
		nullString(row.ContainerID),
		row.NetworkMode,
		nullString(row.CapsJSON),
		nullString(row.Target),
		string(row.Status),
		int64(row.StartedAt),
	))
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("begin tool run %s: %w: %w", row.ID, ErrInvalidToolRun, err)
	}
	return stored, nil
}

// Reject records a run that never started.
//
// Rejection is its own path rather than Finish with a status argument: a rejected
// run must leave no container id and no exit code, and enforcing that inside the
// method is what makes the invariant hold for every caller.
func (r *ToolRunRepo) Reject(ctx context.Context, row ToolRunRow, reason string) (ToolRunRow, error) {
	row.Status = RunRejected
	row.ContainerID = ""
	row.ExitCode = 0
	row.Error = reason

	if err := row.Validate(); err != nil {
		return ToolRunRow{}, err
	}

	const query = `INSERT INTO tool_runs (` + toolRunColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, NULL, NULL, NULL, ?, ?, 0, NULL, ?)
		RETURNING ` + toolRunColumns

	stored, err := scanToolRun(r.db.Writer().QueryRowContext(ctx, query,
		row.ID.String(),
		row.EngagementID.String(),
		nullID(row.StageRunID),
		nullID(row.LLMCallID),
		nullID(row.ApprovalID),
		row.ToolID,
		row.ArgvJSON,
		row.ArgvDisplay,
		row.Image,
		nullString(row.ImageDigest),
		row.NetworkMode,
		nullString(row.CapsJSON),
		nullString(row.Target),
		string(row.Status),
		int64(row.StartedAt),
		int64(row.FinishedAt),
		nullString(reason),
	))
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("reject tool run %s: %w: %w", row.ID, ErrInvalidToolRun, err)
	}
	return stored, nil
}

// Finish closes a run with its outcome.
func (r *ToolRunRepo) Finish(ctx context.Context, id domain.ID, outcome ToolRunRow) (ToolRunRow, error) {
	if !outcome.Status.Terminal() || outcome.Status == RunRejected {
		return ToolRunRow{}, fmt.Errorf("status %q cannot finish a run: %w", outcome.Status, ErrInvalidToolRun)
	}
	if outcome.FinishedAt.IsZero() {
		return ToolRunRow{}, fmt.Errorf("finished_at: %w", ErrInvalidToolRun)
	}

	const query = `
		UPDATE tool_runs
		SET status = ?, exit_code = ?, container_id = ?, output_preview = ?,
		    output_sha256 = ?, finished_at = ?, duration_ms = ?, parsed_json = ?, error = ?
		WHERE id = ?
		RETURNING ` + toolRunColumns

	stored, err := scanToolRun(r.db.Writer().QueryRowContext(ctx, query,
		string(outcome.Status),
		nullableExitCode(outcome.ExitCode, outcome.Status),
		nullString(outcome.ContainerID),
		nullString(outcome.OutputPreview),
		nullString(outcome.OutputSHA256),
		int64(outcome.FinishedAt),
		outcome.DurationMs,
		nullString(outcome.ParsedJSON),
		nullString(outcome.Error),
		id.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolRunRow{}, fmt.Errorf("tool run %s: %w", id, ErrToolRunMissing)
	}
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("finish tool run %s: %w", id, err)
	}
	return stored, nil
}

// Get reads one run by identifier.
func (r *ToolRunRepo) Get(ctx context.Context, id domain.ID) (ToolRunRow, error) {
	const query = `SELECT ` + toolRunColumns + ` FROM tool_runs WHERE id = ?`

	stored, err := scanToolRun(r.db.Reader().QueryRowContext(ctx, query, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolRunRow{}, fmt.Errorf("tool run %s: %w", id, ErrToolRunMissing)
	}
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("read tool run %s: %w", id, err)
	}
	return stored, nil
}

// ListByEngagement returns every run of an engagement, oldest first.
func (r *ToolRunRepo) ListByEngagement(ctx context.Context, engagementID domain.ID) ([]ToolRunRow, error) {
	const query = `SELECT ` + toolRunColumns + `
		FROM tool_runs WHERE engagement_id = ?
		ORDER BY started_at ASC, id ASC`

	rows, err := r.db.Reader().QueryContext(ctx, query, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list tool runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ToolRunRow
	for rows.Next() {
		row, err := scanToolRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tool run: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool runs: %w", err)
	}
	return out, nil
}

// CountByStatus reports how many runs of an engagement ended in a status.
func (r *ToolRunRepo) CountByStatus(ctx context.Context, engagementID domain.ID, status ToolRunStatus) (int, error) {
	if !status.Valid() {
		return 0, fmt.Errorf("status %q: %w", status, ErrInvalidToolRun)
	}

	const query = `SELECT COUNT(*) FROM tool_runs WHERE engagement_id = ? AND status = ?`

	var count int
	if err := r.db.Reader().QueryRowContext(ctx, query, engagementID.String(), string(status)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count tool runs: %w", err)
	}
	return count, nil
}

// scanToolRun reads one row in toolRunColumns order.
func scanToolRun(scanner rowScanner) (ToolRunRow, error) {
	var (
		row          ToolRunRow
		idText       string
		engagementTx string
		stageRunID   sql.NullString
		llmCallID    sql.NullString
		approvalID   sql.NullString
		imageDigest  sql.NullString
		containerID  sql.NullString
		capsJSON     sql.NullString
		target       sql.NullString
		status       string
		exitCode     sql.NullInt64
		outputPrev   sql.NullString
		outputSHA    sql.NullString
		startedAt    int64
		finishedAt   sql.NullInt64
		durationMs   sql.NullInt64
		parsedJSON   sql.NullString
		errText      sql.NullString
	)

	if err := scanner.Scan(
		&idText, &engagementTx, &stageRunID, &llmCallID, &approvalID,
		&row.ToolID, &row.ArgvJSON, &row.ArgvDisplay, &row.Image, &imageDigest, &containerID,
		&row.NetworkMode, &capsJSON, &target, &status, &exitCode,
		&outputPrev, &outputSHA, &startedAt, &finishedAt, &durationMs,
		&parsedJSON, &errText,
	); err != nil {
		return ToolRunRow{}, err
	}

	id, err := domain.NewID(idText)
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("tool run id %q: %w", idText, err)
	}
	engagementID, err := domain.NewID(engagementTx)
	if err != nil {
		return ToolRunRow{}, fmt.Errorf("tool run %s engagement id %q: %w", idText, engagementTx, err)
	}

	row.ID = id
	row.EngagementID = engagementID
	row.StageRunID = parseOptionalID(stageRunID)
	row.LLMCallID = parseOptionalID(llmCallID)
	row.ApprovalID = parseOptionalID(approvalID)
	row.ImageDigest = imageDigest.String
	row.ContainerID = containerID.String
	row.CapsJSON = capsJSON.String
	row.Target = target.String
	row.Status = ToolRunStatus(status)
	row.ExitCode = int(exitCode.Int64)
	row.OutputPreview = outputPrev.String
	row.OutputSHA256 = outputSHA.String
	row.StartedAt = domain.TimestampMs(startedAt)
	row.FinishedAt = domain.TimestampMs(finishedAt.Int64)
	row.DurationMs = durationMs.Int64
	row.ParsedJSON = parsedJSON.String
	row.Error = errText.String

	return row, nil
}

// nullableExitCode keeps exit_code NULL for a run that never produced one.
//
// A timeout or cancellation has no meaningful exit status, and writing 0 would
// read as success to anything that only checks the column.
func nullableExitCode(code int, status ToolRunStatus) any {
	switch status {
	case RunSucceeded, RunFailed:
		return code
	case RunQueued, RunRunning, RunTimeout, RunCancelled, RunRejected:
		return nil
	}
	return nil
}
