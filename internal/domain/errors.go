package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyMessage is returned when a required human-facing message is blank.
var ErrEmptyMessage = errors.New("domain: message is empty")

// wrapUnknown builds a typed error for an unrecognised enum value.
//
// Every enum in this package rejects unknown values at the boundary. Centralising
// the message keeps the wording identical across all of them.
func wrapUnknown(kind, value string) error {
	return fmt.Errorf("%s %q: %w", kind, value, ErrUnknownValue)
}

// ErrUnknownValue is the sentinel behind every unrecognised enum error.
var ErrUnknownValue = errors.New("domain: unknown value")

// requireMessage rejects blank or whitespace-only event messages.
func requireMessage(message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("event message: %w", ErrEmptyMessage)
	}
	return nil
}
