package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/llm"
)

func Test_ListModels_returns_sorted_unique_ids_when_the_gateway_responds(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"cutad/deepseek-v4.1-flash","object":"model","owned_by":"gateway"},
			{"id":"astral-projection","object":"model","owned_by":"gateway"},
			{"id":"cutad/deepseek-v4.1-flash","object":"model","owned_by":"gateway"}
		]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	models, err := client.ListModels(context.Background())

	// Then
	require.NoError(t, err)
	assert.Equal(t, []string{"astral-projection", "cutad/deepseek-v4.1-flash"}, models,
		"ids must be sorted ascending and de-duplicated")
}

func Test_ListModels_returns_a_classified_error_when_the_gateway_is_unreachable(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close()

	client, err := New(Options{BaseURL: baseURL, Timeout: time.Second})
	require.NoError(t, err)

	// When
	_, listErr := client.ListModels(context.Background())

	// Then
	require.Error(t, listErr)
	assert.ErrorIs(t, listErr, llm.ErrRetryable, "a transport failure must stay retryable")
}

func Test_ListModels_requests_the_models_path_when_the_base_url_has_a_version_suffix(t *testing.T) {
	// Given
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"astral-projection"}]}`))
	}))
	defer srv.Close()

	client, err := New(Options{BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)

	// When
	_, err = client.ListModels(context.Background())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "/v1/models", seen, "the version suffix must not be duplicated or dropped")
}

func Test_ListModels_bounds_the_response_when_the_body_is_oversized(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"`))
		_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBytes)))
		_, _ = w.Write([]byte(`"}]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.ListModels(context.Background())

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func Test_ListModels_classifies_a_non_2xx_as_not_retryable_when_the_gateway_rejects_the_key(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)

	// When
	_, err := client.ListModels(context.Background())

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, llm.ErrNotRetryable)

	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusUnauthorized, statusErr.StatusCode)
}

func Test_ListModels_sends_the_bearer_token_when_a_key_is_configured(t *testing.T) {
	// Given
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	client, err := New(Options{BaseURL: srv.URL, APIKey: "sk-test-value"})
	require.NoError(t, err)

	// When
	_, err = client.ListModels(context.Background())

	// Then
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test-value", seen)
}
