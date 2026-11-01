package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

// allEventKinds mirrors the persisted enum from docs/ERD.md §3.22.
var allEventKinds = []domain.EventKind{
	domain.EventStageStart,
	domain.EventStageEnd,
	domain.EventToolStart,
	domain.EventToolEnd,
	domain.EventLLMCall,
	domain.EventFactSet,
	domain.EventFindingAdded,
	domain.EventApprovalRequest,
	domain.EventApprovalDecided,
	domain.EventSessionOpened,
	domain.EventError,
	domain.EventNote,
	domain.EventOperatorCommand,
}

// allLevels mirrors the persisted enum from docs/ERD.md §3.22.
var allLevels = []domain.Level{
	domain.LevelDebug,
	domain.LevelInfo,
	domain.LevelWarn,
	domain.LevelError,
}

func validEventParams() domain.NewEventParams {
	return domain.NewEventParams{
		EngagementID: domain.ID{},
		Stage:        domain.StageRecon,
		SessionID:    domain.ID{},
		ToolRunID:    domain.ID{},
		Kind:         domain.EventStageStart,
		Level:        domain.LevelInfo,
		Message:      "recon stage started",
		DetailJSON:   `{"iteration":1}`,
		Now:          refTime,
	}
}

func Test_EventKind_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name string
		kind domain.EventKind
		want bool
	}{
		{name: "empty", kind: domain.EventKind(""), want: false},
		{name: "unknown", kind: domain.EventKind("bogus"), want: false},
		{name: "wrong case", kind: domain.EventKind("Stage_Start"), want: false},
		{name: "truncated", kind: domain.EventKind("stage_star"), want: false},
		{name: "padded", kind: domain.EventKind(" stage_start "), want: false},
	}

	for _, kind := range allEventKinds {
		tests = append(tests, struct {
			name string
			kind domain.EventKind
			want bool
		}{name: "declared " + string(kind), kind: kind, want: true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			kind := tt.kind

			// When
			got := kind.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_EventKind_AllKinds_AreValidUniqueAndPersisted_when_Enumerated(t *testing.T) {
	// Given
	kinds := allEventKinds

	// When & Then
	require.Len(t, kinds, 13)

	seen := make(map[domain.EventKind]struct{}, len(kinds))
	for _, kind := range kinds {
		require.True(t, kind.Valid(), "event kind %q must be valid", kind)
		require.NotEmpty(t, string(kind))

		_, duplicate := seen[kind]
		require.False(t, duplicate, "event kind %q must appear once", kind)
		seen[kind] = struct{}{}
	}
}

func Test_Level_Valid_Reports_when_ValueIsDeclared(t *testing.T) {
	tests := []struct {
		name  string
		level domain.Level
		want  bool
	}{
		{name: "empty", level: domain.Level(""), want: false},
		{name: "unknown", level: domain.Level("bogus"), want: false},
		{name: "wrong case", level: domain.Level("INFO"), want: false},
		{name: "tracing is not a level", level: domain.Level("trace"), want: false},
		{name: "fatal is not a level", level: domain.Level("fatal"), want: false},
	}

	for _, level := range allLevels {
		tests = append(tests, struct {
			name  string
			level domain.Level
			want  bool
		}{name: "declared " + string(level), level: level, want: true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			level := tt.level

			// When
			got := level.Valid()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_Level_AllLevels_AreValidUnique_when_Enumerated(t *testing.T) {
	// Given
	levels := allLevels

	// When & Then
	require.Len(t, levels, 4)

	seen := make(map[domain.Level]struct{}, len(levels))
	for _, level := range levels {
		require.True(t, level.Valid(), "level %q must be valid", level)

		_, duplicate := seen[level]
		require.False(t, duplicate, "level %q must appear once", level)
		seen[level] = struct{}{}
	}
}

func Test_NewEvent_BuildsEvent_when_ParametersAreValid(t *testing.T) {
	// Given
	engagementID := mustID(t, ulidA)
	sessionID := mustID(t, ulidB)
	toolRunID := mustID(t, ulidC)

	p := domain.NewEventParams{
		EngagementID: engagementID,
		Stage:        domain.StageExploit,
		SessionID:    sessionID,
		ToolRunID:    toolRunID,
		Kind:         domain.EventToolStart,
		Level:        domain.LevelWarn,
		Message:      "launching sqlmap",
		DetailJSON:   `{"argv":["--batch"]}`,
		Now:          refTime,
	}

	// When
	ev, err := domain.NewEvent(p)

	// Then
	require.NoError(t, err)

	require.Equal(t, engagementID, ev.EngagementID)
	require.Equal(t, sessionID, ev.SessionID)
	require.Equal(t, toolRunID, ev.ToolRunID)
	require.Equal(t, domain.StageExploit, ev.Stage)
	require.Equal(t, domain.EventToolStart, ev.Kind)
	require.Equal(t, domain.LevelWarn, ev.Level)
	require.Equal(t, "launching sqlmap", ev.Message)
	require.JSONEq(t, `{"argv":["--batch"]}`, ev.DetailJSON)
	require.Equal(t, refTime, ev.CreatedAt)
	require.True(t, ev.ID.IsZero(), "the store assigns the identifier")
	require.True(t, ev.CreatedAt.Time().Equal(refTime.Time()))
}

func Test_NewEvent_DefaultsDetailJSON_when_DetailIsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		detail string
		want   string
	}{
		{name: "empty", detail: "", want: "{}"},
		{name: "explicit empty object", detail: "{}", want: "{}"},
		{name: "explicit payload", detail: `{"k":"v"}`, want: `{"k":"v"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEventParams()
			p.DetailJSON = tt.detail

			// When
			ev, err := domain.NewEvent(p)

			// Then
			require.NoError(t, err)
			assert.Equal(t, tt.want, ev.DetailJSON)
		})
	}
}

func Test_NewEvent_PreservesMessage_when_MessageIsPadded(t *testing.T) {
	// Given
	p := validEventParams()
	p.Message = "  recon stage started  "

	// When
	ev, err := domain.NewEvent(p)

	// Then
	require.NoError(t, err)
	require.Equal(t, "  recon stage started  ", ev.Message, "messages are stored verbatim")
}

func Test_NewEvent_AcceptsEveryKindAndLevel_when_OtherInputsAreValid(t *testing.T) {
	// Given
	p := validEventParams()

	// When & Then
	for _, kind := range allEventKinds {
		for _, level := range allLevels {
			p.Kind = kind
			p.Level = level

			ev, err := domain.NewEvent(p)
			require.NoError(t, err, "kind %s level %s", kind, level)
			require.Equal(t, kind, ev.Kind)
			require.Equal(t, level, ev.Level)
		}
	}
}
