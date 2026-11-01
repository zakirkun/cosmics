package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/llm"
)

// roundTrip sends the request, retrying only failures that can succeed later.
func (c *Client) roundTrip(ctx context.Context, body []byte, stream bool) (*http.Response, int, error) {
	attempts := c.retries + 1
	var lastErr error

	for attempt := range attempts {
		if err := ctx.Err(); err != nil {
			return nil, attempt, fmt.Errorf("request cancelled after %d attempt(s): %w", attempt, err)
		}

		resp, err := c.send(ctx, body, stream)
		if err == nil {
			return resp, attempt + 1, nil
		}
		lastErr = err

		if !errors.Is(err, llm.ErrRetryable) || attempt == attempts-1 {
			break
		}

		delay := backoffDelay(attempt, retryAfter(err))
		if sleepErr := c.sleep(ctx, delay); sleepErr != nil {
			return nil, attempt + 1, fmt.Errorf("retry wait: %w", sleepErr)
		}
	}

	return nil, attempts, lastErr
}

// send performs exactly one HTTP exchange.
func (c *Client) send(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request failed: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", llm.ErrRetryable, err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	decoded, decodeErr := decodeError(resp)
	if decodeErr != nil {
		return nil, decodeErr
	}
	return nil, decoded
}

// decodeError turns a non-2xx response into a classified typed error.
func decodeError(resp *http.Response) (error, error) {
	defer func() { _ = resp.Body.Close() }()

	payload, err := readLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read error body: %w", err)
	}

	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))

	var envelope wireErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Error.Message == "" {
		return newStatusError(resp.StatusCode, wireError{
			Message: strings.TrimSpace(string(payload)),
			Type:    "unrecognised_error_body",
		}, retryAfter), nil
	}

	return newStatusError(resp.StatusCode, envelope.Error, retryAfter), nil
}

// readLimited reads a body up to maxResponseBytes and reports truncation.
func readLimited(reader io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return payload, nil
}

// backoffDelay computes the wait before the next attempt.
func backoffDelay(attempt int, honoured time.Duration) time.Duration {
	if honoured > 0 {
		return honoured
	}

	delay := baseRetryDelay << attempt
	if delay > maxRetryDelay || delay <= 0 {
		return maxRetryDelay
	}
	return delay
}

// parseRetryAfter accepts both delta-seconds and an HTTP date.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	if when, err := http.ParseTime(value); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 0
}

// retryAfter extracts a server-directed wait from a classified error.
func retryAfter(err error) time.Duration {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.RetryAfter
	}
	return 0
}

// sleepContext waits, aborting early if the context is cancelled.
func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
