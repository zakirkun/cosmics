package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cosmics/cosmics/internal/llm"
)

// modelsPath is appended to the client base URL. The base already carries the
// API version, so the path must not repeat it.
const modelsPath = "/models"

// wireModel is one entry of the gateway's model catalogue.
type wireModel struct {
	ID string `json:"id"`
}

// wireModelsResponse is the OpenAI-compatible model listing envelope.
type wireModelsResponse struct {
	Data  []wireModel `json:"data"`
	Error *wireError  `json:"error,omitempty"`
}

// ListModels returns the sorted, de-duplicated identifiers the gateway
// advertises, so onboarding can offer the operator a live list instead of a
// hardcoded guess.
//
// The request targets the same base URL the client was built with, carries the
// same bearer token as a completion, and classifies a non-2xx response through
// the shared StatusError path so retry policy stays consistent.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.modelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("list models: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: %w", llm.ErrRetryable, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		classified, decodeErr := decodeError(resp)
		if decodeErr != nil {
			return nil, fmt.Errorf("list models: %w", decodeErr)
		}
		return nil, fmt.Errorf("list models: %w", classified)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := readLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}

	var decoded wireModelsResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	if decoded.Error != nil && len(decoded.Data) == 0 {
		return nil, fmt.Errorf("list models: %w", newStatusError(resp.StatusCode, *decoded.Error, 0))
	}

	seen := make(map[string]struct{}, len(decoded.Data))
	ids := make([]string, 0, len(decoded.Data))
	for _, model := range decoded.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	slices.Sort(ids)
	return ids, nil
}
