package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// ErrEmbeddingUnavailable is returned when vector or hybrid retrieval is
// requested and no embedder was configured.
//
// It is deliberately an error and not a fallback to keyword search: the operator
// must know the agent saw keyword results only (docs/ARCHITECTURE.md section 8.1).
var ErrEmbeddingUnavailable = errors.New("memory: embedding provider is not configured")

// RetrievalMode selects the scoring path.
type RetrievalMode string

// Retrieval modes. Keyword is the default because no embedding source exists.
const (
	RetrievalKeyword RetrievalMode = "keyword"
	RetrievalVector  RetrievalMode = "vector"
	RetrievalHybrid  RetrievalMode = "hybrid"
)

// AllRetrievalModes lists every declared mode, for exhaustiveness tests.
var AllRetrievalModes = []RetrievalMode{RetrievalKeyword, RetrievalVector, RetrievalHybrid}

// Valid reports whether the mode is one of the declared constants.
func (m RetrievalMode) Valid() bool {
	switch m {
	case RetrievalKeyword, RetrievalVector, RetrievalHybrid:
		return true
	}
	return false
}

// Embedder turns text into dense vectors. Implementations live above this
// package; v1 ships none, which is why vector modes report
// ErrEmbeddingUnavailable.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Query describes one scoped retrieval request.
//
// Stage is carried for the caller's scope and for the rerank described in
// docs/ARCHITECTURE.md section 8.2; the keyword path orders strictly by bm25 so
// a repeated query returns the same list.
type Query struct {
	EngagementID domain.ID
	Text         string
	Kinds        []string
	Stage        domain.StageKind
	TopK         int
	TokenBudget  int
	Mode         RetrievalMode
}

// Hit is one ranked memory chunk.
//
// Score is positive-is-better: FTS5 bm25 returns negative values where more
// negative is a better match, so the keyword path negates it.
type Hit struct {
	SourceKind string
	SourceID   string
	Text       string
	Score      float64
	Tokens     int
}

// Retriever ranks memory chunks for context assembly.
type Retriever struct {
	db       *store.DB
	embedder Embedder
}

// NewRetriever returns a keyword-only retriever. Vector modes report
// ErrEmbeddingUnavailable.
func NewRetriever(db *store.DB) *Retriever {
	return &Retriever{db: db}
}

// NewRetrieverWithEmbedder returns a retriever that can also score vectors.
// A nil embedder is equivalent to NewRetriever.
func NewRetrieverWithEmbedder(db *store.DB, embedder Embedder) *Retriever {
	return &Retriever{db: db, embedder: embedder}
}

// Search returns at most Query.TopK hits inside Query.TokenBudget.
func (r *Retriever) Search(ctx context.Context, q Query) ([]Hit, error) {
	mode := q.Mode
	if mode == "" {
		mode = RetrievalKeyword
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("search %q: retrieval mode %q is unknown", q.Text, q.Mode)
	}
	if mode != RetrievalKeyword && r.embedder == nil {
		return nil, fmt.Errorf("search %q in %s mode: %w", q.Text, mode, ErrEmbeddingUnavailable)
	}

	var (
		chunks []chunk
		err    error
	)
	switch mode {
	case RetrievalKeyword:
		chunks, err = r.keywordChunks(ctx, q, candidateLimit)
	case RetrievalVector:
		chunks, err = r.vectorChunks(ctx, q, candidateLimit)
	case RetrievalHybrid:
		chunks, err = r.hybridChunks(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("search %q in %s mode: %w", q.Text, mode, err)
	}

	return applyBudget(hitsFrom(chunks), q), nil
}

// chunk is one memory_chunks row plus whatever score the active path computed.
type chunk struct {
	sourceKind string
	sourceID   string
	text       string
	tokens     int
	score      float64
	embedding  []float32
}

// keywordChunks runs the FTS5 bm25 query scoped to an engagement.
//
// Tokens are OR-ed rather than AND-ed: bm25 supplies the ranking, and an
// all-terms-required query returns nothing for a natural-language probe. bm25 is
// negated so Hit.Score grows with relevance.
func (r *Retriever) keywordChunks(ctx context.Context, q Query, limit int) ([]chunk, error) {
	match := ftsQuery(q.Text)
	if match == "" {
		return nil, nil
	}

	where, scopeArgs := scopeClause(q)
	args := make([]any, 0, len(scopeArgs)+2)
	args = append(args, match)
	args = append(args, scopeArgs...)
	args = append(args, limit)

	query := `SELECT c.source_kind, c.source_id, c.text, c.token_estimate,
	                 bm25(chunks_fts) AS rank
	            FROM chunks_fts
	            JOIN memory_chunks c ON c.rowid = chunks_fts.rowid
	           WHERE chunks_fts MATCH ? AND ` + where + `
	           ORDER BY rank ASC, c.rowid ASC
	           LIMIT ?`

	rows, err := r.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query chunks_fts: %w", err)
	}
	defer rows.Close()

	chunks := make([]chunk, 0, min(limit, 32))
	for rows.Next() {
		var (
			c    chunk
			rank float64
		)
		if err := rows.Scan(&c.sourceKind, &c.sourceID, &c.text, &c.tokens, &rank); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		c.score = -rank
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunks: %w", err)
	}
	return chunks, nil
}

// vectorChunks embeds the query and scores every stored vector in scope.
func (r *Retriever) vectorChunks(ctx context.Context, q Query, limit int) ([]chunk, error) {
	vectors, err := r.embedder.Embed(ctx, []string{q.Text})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embed query: expected 1 vector, got %d", len(vectors))
	}
	queryVector := vectors[0]

	candidates, err := r.storedChunks(ctx, q)
	if err != nil {
		return nil, err
	}

	scored := make([]chunk, 0, len(candidates))
	for _, c := range candidates {
		similarity, err := CosineSimilarity(queryVector, c.embedding)
		if err != nil {
			return nil, fmt.Errorf("score chunk %s/%s: %w", c.sourceKind, c.sourceID, err)
		}
		c.score = similarity
		scored = append(scored, c)
	}
	sortChunks(scored)
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// hybridChunks fuses the keyword and vector rankings with reciprocal rank fusion.
func (r *Retriever) hybridChunks(ctx context.Context, q Query) ([]chunk, error) {
	keyword, err := r.keywordChunks(ctx, q, candidateLimit)
	if err != nil {
		return nil, err
	}
	vector, err := r.vectorChunks(ctx, q, candidateLimit)
	if err != nil {
		return nil, err
	}

	fused := make(map[string]chunk, len(keyword)+len(vector))
	fuse := func(list []chunk) {
		for rank, c := range list {
			key := c.sourceKind + "\x00" + c.sourceID
			entry := fused[key]
			entry.sourceKind = c.sourceKind
			entry.sourceID = c.sourceID
			entry.text = c.text
			entry.tokens = c.tokens
			entry.score += 1 / float64(rrfK+rank+1)
			fused[key] = entry
		}
	}
	fuse(keyword)
	fuse(vector)

	merged := make([]chunk, 0, len(fused))
	for _, c := range fused {
		merged = append(merged, c)
	}
	sortChunks(merged)
	return merged, nil
}

// storedChunks loads every in-scope chunk that carries an embedding.
func (r *Retriever) storedChunks(ctx context.Context, q Query) ([]chunk, error) {
	where, scopeArgs := scopeClause(q)
	query := `SELECT c.source_kind, c.source_id, c.text, c.token_estimate, c.embedding
	            FROM memory_chunks c
	           WHERE ` + where + ` AND c.embedding IS NOT NULL`

	rows, err := r.db.Reader().QueryContext(ctx, query, scopeArgs...)
	if err != nil {
		return nil, fmt.Errorf("query chunk embeddings: %w", err)
	}
	defer rows.Close()

	chunks := make([]chunk, 0, 32)
	for rows.Next() {
		var (
			c    chunk
			blob []byte
		)
		if err := rows.Scan(&c.sourceKind, &c.sourceID, &c.text, &c.tokens, &blob); err != nil {
			return nil, fmt.Errorf("scan chunk embedding: %w", err)
		}
		vector, err := decodeEmbedding(blob)
		if err != nil {
			return nil, fmt.Errorf("decode chunk %s/%s embedding: %w", c.sourceKind, c.sourceID, err)
		}
		c.embedding = vector
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunk embeddings: %w", err)
	}
	return chunks, nil
}
