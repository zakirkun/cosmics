package anthropic

import (
	"fmt"
	"time"

	"github.com/cosmics/cosmics/internal/llm"
)

// StatusError is a non-2xx provider response.
type StatusError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

// Error renders the status and provider message.
func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("anthropic: http %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("anthropic: http %d: %s", e.StatusCode, e.Message)
}

// Unwrap classifies the error for errors.Is.
func (e *StatusError) Unwrap() error {
	return llm.Classify(e.StatusCode, e.Code)
}

// newStatusError builds a classified error from a decoded provider payload.
func newStatusError(status int, payload wireError, retryAfter time.Duration) *StatusError {
	return &StatusError{
		StatusCode: status,
		Code:       payload.Type,
		Message:    payload.Message,
		RetryAfter: retryAfter,
	}
}
