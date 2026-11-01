package domain

// EventKind classifies a row in the append-only event log.
type EventKind string

// Event kinds. These strings are persisted and are never renamed.
const (
	EventStageStart      EventKind = "stage_start"
	EventStageEnd        EventKind = "stage_end"
	EventToolStart       EventKind = "tool_start"
	EventToolEnd         EventKind = "tool_end"
	EventLLMCall         EventKind = "llm_call"
	EventFactSet         EventKind = "fact_set"
	EventFindingAdded    EventKind = "finding_added"
	EventApprovalRequest EventKind = "approval_requested"
	EventApprovalDecided EventKind = "approval_decided"
	EventSessionOpened   EventKind = "session_opened"
	EventError           EventKind = "error"
	EventNote            EventKind = "note"
	EventOperatorCommand EventKind = "operator_command"
)

// Valid reports whether the kind is a declared constant.
func (k EventKind) Valid() bool {
	switch k {
	case EventStageStart, EventStageEnd, EventToolStart, EventToolEnd, EventLLMCall,
		EventFactSet, EventFindingAdded, EventApprovalRequest, EventApprovalDecided,
		EventSessionOpened, EventError, EventNote, EventOperatorCommand:
		return true
	}
	return false
}

// Level is the severity of a logged event.
type Level string

// Event levels.
const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Valid reports whether the level is a declared constant.
func (l Level) Valid() bool {
	switch l {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
		return true
	}
	return false
}

// Event is one immutable record in the engagement narrative. It is the
// episodic memory the agent reads back when rebuilding context.
type Event struct {
	ID           ID
	EngagementID ID
	Stage        StageKind
	SessionID    ID
	ToolRunID    ID
	Kind         EventKind
	Level        Level
	Message      string
	DetailJSON   string
	CreatedAt    TimestampMs
}

// NewEventParams groups event construction inputs.
type NewEventParams struct {
	EngagementID ID
	Stage        StageKind
	SessionID    ID
	ToolRunID    ID
	Kind         EventKind
	Level        Level
	Message      string
	DetailJSON   string
	Now          TimestampMs
}

// NewEvent validates and constructs an event row.
func NewEvent(p NewEventParams) (Event, error) {
	if !p.Kind.Valid() {
		return Event{}, wrapUnknown("event kind", string(p.Kind))
	}
	if !p.Level.Valid() {
		return Event{}, wrapUnknown("event level", string(p.Level))
	}
	if err := requireMessage(p.Message); err != nil {
		return Event{}, err
	}

	detail := p.DetailJSON
	if detail == "" {
		detail = "{}"
	}

	return Event{
		EngagementID: p.EngagementID,
		Stage:        p.Stage,
		SessionID:    p.SessionID,
		ToolRunID:    p.ToolRunID,
		Kind:         p.Kind,
		Level:        p.Level,
		Message:      p.Message,
		DetailJSON:   detail,
		CreatedAt:    p.Now,
	}, nil
}
