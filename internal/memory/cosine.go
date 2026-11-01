package memory

import (
	"encoding/binary"
	"fmt"
	"math"
)

// decodeEmbedding unpacks a little-endian float32 blob (ERD section 3.21).
func decodeEmbedding(blob []byte) ([]float32, error) {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil, fmt.Errorf("embedding blob of %d bytes is not a float32 sequence", len(blob))
	}
	vector := make([]float32, len(blob)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vector, nil
}

// CosineSimilarity returns the cosine of the angle between a and b.
//
// Both vectors must have the same non-zero dimension. A zero vector has no
// direction, so it scores 0 rather than producing NaN.
func CosineSimilarity(a, b []float32) (float64, error) {
	if len(a) == 0 {
		return 0, fmt.Errorf("cosine similarity: left vector is empty")
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("cosine similarity: dimension mismatch: %d != %d", len(a), len(b))
	}

	var dot, normA, normB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}
