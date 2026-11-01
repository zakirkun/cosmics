package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// EventRepo appends to and reads the append-only event log.
type EventRepo struct {
	db *DB
}

// NewEventRepo returns a repository over db.
func NewEventRepo(db *DB) *EventRepo {
	return &EventRepo{db: db}
}

const eventColumns = `id, engagement_id, stage, session_id, tool_run_id, kind, level,
	message, detail_json, created_at`

const insertEventSQL = `INSERT INTO events (` + eventColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Append writes one event row.
//
// The caller supplies the ID and CreatedAt: the log is the engagement's
// chronology, so its ordering must reflect when the thing happened, not when
// the insert ran. The insert is a single statement, so it needs no explicit
// transaction.
func (r *EventRepo) Append(ctx context.Context, event domain.Event) error {
	if event.ID.IsZero() {
		return fmt.Errorf("append event %q: %w", event.Kind, domain.ErrEmptyID)
	}
	if event.EngagementID.IsZero() {
		return fmt.Errorf("append event %q: engagement id: %w", event.Kind, domain.ErrEmptyID)
	}

	_, err := r.db.Writer().ExecContext(ctx, insertEventSQL,
		event.ID.String(), event.EngagementID.String(), nullStage(event.Stage),
		nullID(event.SessionID), nullID(event.ToolRunID),
		string(event.Kind), string(event.Level), event.Message,
		nullString(event.DetailJSON), int64(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("append event %q for engagement %q: %w", event.Kind, event.EngagementID, err)
	}
	return nil
}

// ListByEngagement returns the newest limit events for an engagement.
//
// A limit of zero or less means "no limit"; the SQL uses -1, which SQLite reads
// as unlimited.
func (r *EventRepo) ListByEngagement(ctx context.Context, engagementID domain.ID, limit int) ([]domain.Event, error) {
	return r.queryEvents(ctx,
		`SELECT `+eventColumns+` FROM events WHERE engagement_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		engagementID.String(), normalizeLimit(limit))
}

// ListByEngagementAndLevel returns the newest limit events at one level.
func (r *EventRepo) ListByEngagementAndLevel(
	ctx context.Context,
	engagementID domain.ID,
	level domain.Level,
	limit int,
) ([]domain.Event, error) {
	return r.queryEvents(ctx,
		`SELECT `+eventColumns+` FROM events WHERE engagement_id = ? AND level = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		engagementID.String(), string(level), normalizeLimit(limit))
}

// queryEvents runs a projection over eventColumns and scans every row.
func (r *EventRepo) queryEvents(ctx context.Context, query string, args ...any) ([]domain.Event, error) {
	rows, err := r.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.Event, 0, 32)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// scanEvent maps one eventColumns row into a domain value.
func scanEvent(scanner rowScanner) (domain.Event, error) {
	var (
		event            domain.Event
		id, engagementID string
		stage, sessionID sql.NullString
		toolRunID        sql.NullString
		kind, level      string
		detailJSON       sql.NullString
		createdAt        int64
	)

	err := scanner.Scan(&id, &engagementID, &stage, &sessionID, &toolRunID,
		&kind, &level, &event.Message, &detailJSON, &createdAt)
	if err != nil {
		return domain.Event{}, err
	}

	parsedID, err := domain.NewID(id)
	if err != nil {
		return domain.Event{}, fmt.Errorf("event id: %w", err)
	}
	parsedEngagementID, err := domain.NewID(engagementID)
	if err != nil {
		return domain.Event{}, fmt.Errorf("event engagement id: %w", err)
	}

	event.ID = parsedID
	event.EngagementID = parsedEngagementID
	event.Stage = domain.StageKind(stage.String)
	event.Kind = domain.EventKind(kind)
	event.Level = domain.Level(level)
	event.DetailJSON = detailJSON.String
	event.CreatedAt = domain.TimestampMs(createdAt)

	if event.SessionID, err = optionalID(sessionID); err != nil {
		return domain.Event{}, fmt.Errorf("event session id: %w", err)
	}
	if event.ToolRunID, err = optionalID(toolRunID); err != nil {
		return domain.Event{}, fmt.Errorf("event tool run id: %w", err)
	}
	return event, nil
}

// optionalID parses a nullable identifier column, leaving the zero ID when the
// column is NULL or empty.
func optionalID(value sql.NullString) (domain.ID, error) {
	if !value.Valid || value.String == "" {
		return domain.ID{}, nil
	}
	return domain.NewID(value.String)
}

// nullID stores SQL NULL for an unset identifier.
func nullID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

// nullStage stores SQL NULL for an unset stage. events.stage is nullable
// because an error may occur before any stage is entered.
func nullStage(stage domain.StageKind) any {
	if stage == "" {
		return nil
	}
	return string(stage)
}

// normalizeLimit maps "no limit" onto SQLite's -1.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return -1
	}
	return limit
}
