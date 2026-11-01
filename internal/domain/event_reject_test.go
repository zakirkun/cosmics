package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

func Test_NewEvent_RejectsInput_when_KindIsUndeclared(t *testing.T) {
	tests := []struct {
		name string
		kind domain.EventKind
	}{
		{name: "empty", kind: domain.EventKind("")},
		{name: "unknown", kind: domain.EventKind("bogus")},
		{name: "wrong case", kind: domain.EventKind("STAGE_START")},
		{name: "near miss", kind: domain.EventKind("stage_started")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEventParams()
			p.Kind = tt.kind

			// When
			ev, err := domain.NewEvent(p)

			// Then
			require.ErrorIs(t, err, domain.ErrUnknownValue)
			assert.Equal(t, domain.Event{}, ev)
		})
	}
}

func Test_NewEvent_RejectsInput_when_LevelIsUndeclared(t *testing.T) {
	tests := []struct {
		name  string
		level domain.Level
	}{
		{name: "empty", level: domain.Level("")},
		{name: "unknown", level: domain.Level("bogus")},
		{name: "wrong case", level: domain.Level("Info")},
		{name: "numeric", level: domain.Level("3")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEventParams()
			p.Level = tt.level

			// When
			ev, err := domain.NewEvent(p)

			// Then
			require.ErrorIs(t, err, domain.ErrUnknownValue)
			assert.Equal(t, domain.Event{}, ev)
		})
	}
}

func Test_NewEvent_RejectsInput_when_MessageIsBlank(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{name: "empty", message: ""},
		{name: "spaces", message: "   "},
		{name: "tabs", message: "\t\t"},
		{name: "newlines", message: "\r\n"},
		{name: "mixed whitespace", message: " \t\r\n "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			p := validEventParams()
			p.Message = tt.message

			// When
			ev, err := domain.NewEvent(p)

			// Then
			require.ErrorIs(t, err, domain.ErrEmptyMessage)
			require.NotErrorIs(t, err, domain.ErrUnknownValue)
			assert.Equal(t, domain.Event{}, ev)
		})
	}
}

func Test_NewEvent_ChecksKindBeforeLevel_when_BothAreUndeclared(t *testing.T) {
	// Given
	p := validEventParams()
	p.Kind = domain.EventKind("bogus")
	p.Level = domain.Level("bogus")

	// When
	ev, err := domain.NewEvent(p)

	// Then
	require.ErrorIs(t, err, domain.ErrUnknownValue)
	require.Equal(t, domain.Event{}, ev)
}

func Test_NewEvent_PreservesStage_when_StageIsUnknown(t *testing.T) {
	// Given: StageKind lives on the event row but is a value the store persists
	// verbatim, so the constructor must not alter or reject it.
	p := validEventParams()
	p.Stage = domain.StageKind("bogus")

	// When
	ev, err := domain.NewEvent(p)

	// Then
	require.NoError(t, err)
	require.Equal(t, domain.StageKind("bogus"), ev.Stage)
}
