package memory_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/memory"
	"github.com/cosmics/cosmics/internal/store"
)

func Test_CosineSimilarity_returns_one_when_vectors_are_identical(t *testing.T) {
	// Given: one non-trivial vector.
	vector := []float32{1.5, -2.5, 3.25, 0.5}

	// When: it is compared with itself.
	score, err := memory.CosineSimilarity(vector, vector)

	// Then: the cosine is exactly one.
	require.NoError(t, err)
	assert.InDelta(t, 1.0, score, 1e-9)
}

func Test_CosineSimilarity_returns_zero_when_a_vector_is_all_zeroes(t *testing.T) {
	// Given: a zero vector, which has no direction.
	// When/Then: the score is zero rather than NaN.
	score, err := memory.CosineSimilarity([]float32{0, 0, 0}, []float32{1, 2, 3})
	require.NoError(t, err)
	assert.InDelta(t, 0.0, score, 1e-9)
	assert.False(t, math.IsNaN(score))
}

func Test_CosineSimilarity_returns_an_error_when_dimensions_differ(t *testing.T) {
	// Given: two vectors of different lengths.
	// When: they are compared.
	_, err := memory.CosineSimilarity([]float32{1, 2, 3}, []float32{1, 2})

	// Then: the mismatch is reported, not silently truncated.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimension mismatch")
}

func Test_CosineSimilarity_returns_an_error_when_a_vector_is_empty(t *testing.T) {
	// Given: an empty vector.
	// When/Then: comparison fails instead of dividing by zero.
	_, err := memory.CosineSimilarity(nil, []float32{1})
	require.Error(t, err)
}

// stubEmbedder returns canned vectors so vector mode runs without a provider.
type stubEmbedder struct {
	vectors [][]float32
	err     error
}

// Embed returns the canned vectors.
func (s stubEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.vectors, nil
}

// insertEmbeddedChunk writes a chunk with a packed float32 embedding.
func insertEmbeddedChunk(
	t *testing.T,
	db *store.DB,
	engagementID domain.ID,
	sourceID, text string,
	tokens int,
	vector []float32,
) {
	t.Helper()

	blob := make([]byte, len(vector)*4)
	for i, value := range vector {
		bits := math.Float32bits(value)
		blob[i*4] = byte(bits)
		blob[i*4+1] = byte(bits >> 8)
		blob[i*4+2] = byte(bits >> 16)
		blob[i*4+3] = byte(bits >> 24)
	}

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO memory_chunks
		 (id, engagement_id, source_kind, source_id, chunk_index, text,
		  token_estimate, embedding_model, embedding_dim, embedding, created_at)
		 VALUES (?, ?, 'fact', ?, 0, ?, ?, 'stub', ?, ?, ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(),
		nullID(engagementID), sourceID, text, tokens, len(vector), blob, int64(baseTimestamp))
	require.NoError(t, err)
}
