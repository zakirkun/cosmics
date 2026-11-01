package openai

import (
	"fmt"
	"time"

	"github.com/cosmics/cosmics/internal/llm"
)

// StatusError is a non-2xx provider response.
//
// It wraps either llm.ErrRetryable or llm.ErrNotRetryable so every caller can
// decide failover with errors.Is instead of re-deriving policy from a status code.
type StatusError struct {
	StatusCode int
	Code       string
	Type       string
	Message    string
	RetryAfter time.Duration
}

// Error renders the status and provider message.
func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("openai: http %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("openai: http %d: %s", e.StatusCode, e.Message)
}

// Unwrap classifies the error for errors.Is.
func (e *StatusError) Unwrap() error {
	return llm.Classify(e.StatusCode, e.Code)
}

// newStatusError builds a classified error from a decoded provider payload.
func newStatusError(status int, payload wireError, retryAfter time.Duration) *StatusError {
	return &StatusError{
		StatusCode: status,
		Code:       payload.Code,
		Type:       payload.Type,
		Message:    payload.Message,
		RetryAfter: retryAfter,
	}
}
