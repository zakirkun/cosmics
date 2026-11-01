package memory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/memory"
	"github.com/cosmics/cosmics/internal/store"
)

// insertChunk writes one memory_chunks row directly, so a retrieval test does
// not depend on whichever producer would normally populate the table.
func insertChunk(
	t *testing.T,
	db *store.DB,
	engagementID domain.ID,
	sourceKind, sourceID, text string,
	tokens int,
) {
	t.Helper()

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO memory_chunks
		 (id, engagement_id, source_kind, source_id, chunk_index, text,
		  token_estimate, created_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(),
		nullID(engagementID), sourceKind, sourceID, text, tokens, int64(baseTimestamp))
	require.NoError(t, err)
}

// nullID maps the zero ID onto SQL NULL, which marks a global chunk.
func nullID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

// keywordQuery builds a keyword Query scoped to one engagement.
func keywordQuery(engagementID domain.ID, text string) memory.Query {
	return memory.Query{
		EngagementID: engagementID,
		Text:         text,
		TopK:         10,
		Mode:         memory.RetrievalKeyword,
	}
}

func Test_Search_returns_ranked_hits_when_matching_chunks_exist(t *testing.T) {
	// Given: three chunks about distinct technologies plus one off-topic chunk.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertChunk(t, db, engagementID, "skill", "s-nginx", "nginx reverse proxy configuration hardening", 8)
	insertChunk(t, db, engagementID, "skill", "s-apache", "apache httpd nginx virtual host tuning", 8)
	insertChunk(t, db, engagementID, "skill", "s-nginx-deep", "nginx nginx nginx worker process tuning nginx", 9)
	insertChunk(t, db, engagementID, "skill", "s-unrelated", "certificate transparency log monitoring", 7)

	retriever := memory.NewRetriever(db)

	// When: the query matches the nginx chunks.
	hits, err := retriever.Search(t.Context(), keywordQuery(engagementID, "nginx worker tuning"))

	// Then: matching chunks rank above the unrelated one and every score is
	// positive.
	require.NoError(t, err)
	require.NotEmpty(t, hits)

	for _, hit := range hits {
		assert.Positive(t, hit.Score, "bm25 must be negated so a better match scores higher")
	}
	assert.Equal(t, "s-nginx-deep", hits[0].SourceID, "the densest match ranks first")

	for i := 1; i < len(hits); i++ {
		assert.GreaterOrEqual(t, hits[i-1].Score, hits[i].Score, "hits are ordered best-first")
	}

	assert.False(t, containsSourceID(hits, "s-unrelated"),
		"a chunk with no query term must not be returned at all")
}

func Test_Search_respects_the_token_budget_when_hits_exceed_it(t *testing.T) {
	// Given: five matching chunks of 10 tokens each.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	for i := range 5 {
		insertChunk(t, db, engagementID, "skill", "s-"+string(rune('a'+i)),
			"kubernetes pod security context hardening reference", 10)
	}

	query := keywordQuery(engagementID, "kubernetes pod security")
	query.TokenBudget = 25
	retriever := memory.NewRetriever(db)

	// When: the query runs with a 25-token budget.
	hits, err := retriever.Search(t.Context(), query)

	// Then: the accumulated estimate never exceeds the budget.
	require.NoError(t, err)
	require.NotEmpty(t, hits)

	total := 0
	for _, hit := range hits {
		total += hit.Tokens
	}
	assert.LessOrEqual(t, total, 25, "the returned set must fit the token budget")
	assert.Equal(t, 2, len(hits), "two 10-token hits fit; the third would overflow")
}

func Test_Search_respects_TopK_when_more_hits_are_available(t *testing.T) {
	// Given: six matching chunks and a TopK of three.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	for i := range 6 {
		insertChunk(t, db, engagementID, "skill", "s-"+string(rune('a'+i)),
			"vulnerability assessment methodology reference material", 5)
	}

	query := keywordQuery(engagementID, "vulnerability assessment methodology")
	query.TopK = 3
	retriever := memory.NewRetriever(db)

	// When: the query runs.
	hits, err := retriever.Search(t.Context(), query)

	// Then: exactly TopK hits come back.
	require.NoError(t, err)
	assert.Len(t, hits, 3)
}

func Test_Search_excludes_other_engagements_when_filtered(t *testing.T) {
	// Given: two engagements holding matching chunks.
	db := openTestDB(t)
	mine := newEngagementID()
	theirs := newEngagementID()
	seedEngagement(t, db, mine)
	seedEngagement(t, db, theirs)
	insertChunk(t, db, mine, "fact", "f-mine", "postgres database listening on 5432", 6)
	insertChunk(t, db, theirs, "fact", "f-theirs", "postgres database listening on 5432", 6)

	retriever := memory.NewRetriever(db)

	// When: the query is scoped to one engagement.
	hits, err := retriever.Search(t.Context(), keywordQuery(mine, "postgres 5432"))

	// Then: the other engagement's chunk never appears.
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.True(t, containsSourceID(hits, "f-mine"))
	assert.False(t, containsSourceID(hits, "f-theirs"), "another engagement's memory must not leak")
}

func Test_Search_includes_global_chunks_when_engagement_id_is_null(t *testing.T) {
	// Given: one engagement-scoped chunk and one global skill chunk.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertChunk(t, db, engagementID, "fact", "f-scoped", "smb signing disabled on file server", 7)
	insertChunk(t, db, domain.ID{}, "skill", "s-global", "smb signing enumeration procedure", 7)

	retriever := memory.NewRetriever(db)

	// When: the query is scoped to the engagement.
	hits, err := retriever.Search(t.Context(), keywordQuery(engagementID, "smb signing"))

	// Then: both the scoped fact and the global skill are eligible.
	require.NoError(t, err)
	assert.True(t, containsSourceID(hits, "f-scoped"))
	assert.True(t, containsSourceID(hits, "s-global"),
		"global rows carry no engagement and must stay retrievable")
}

func Test_Search_filters_by_source_kind_when_kinds_are_supplied(t *testing.T) {
	// Given: a skill chunk and a fact chunk matching the same term.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertChunk(t, db, engagementID, "fact", "f-1", "kerberos pre-authentication disabled", 6)
	insertChunk(t, db, engagementID, "skill", "s-1", "kerberos attack path enumeration", 6)

	query := keywordQuery(engagementID, "kerberos")
	query.Kinds = []string{"fact"}

	retriever := memory.NewRetriever(db)

	// When: the query restricts kinds to facts.
	hits, err := retriever.Search(t.Context(), query)

	// Then: only fact chunks are returned.
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	for _, hit := range hits {
		assert.Equal(t, "fact", hit.SourceKind)
	}
}

func Test_Search_returns_no_hits_when_the_text_has_no_terms(t *testing.T) {
	// Given: an engagement with a matching chunk and a punctuation-only query.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertChunk(t, db, engagementID, "fact", "f-1", "postgres listening on 5432", 6)

	retriever := memory.NewRetriever(db)

	// When: the query carries no alphanumeric term.
	hits, err := retriever.Search(t.Context(), keywordQuery(engagementID, "!!! ??? ---"))

	// Then: it is an empty result, not a SQL syntax error.
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func Test_Search_returns_ErrEmbeddingUnavailable_when_vector_mode_has_no_embedder(t *testing.T) {
	// Given: a keyword-only retriever.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertChunk(t, db, engagementID, "fact", "f-1", "postgres listening on 5432", 6)
	retriever := memory.NewRetriever(db)

	query := keywordQuery(engagementID, "postgres")
	query.Mode = memory.RetrievalVector

	// When: vector mode is requested with no embedder configured.
	hits, err := retriever.Search(t.Context(), query)

	// Then: it fails loudly instead of silently degrading to keyword.
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrEmbeddingUnavailable)
	assert.Nil(t, hits)
}

func Test_Search_returns_ErrEmbeddingUnavailable_when_hybrid_mode_has_no_embedder(t *testing.T) {
	// Given: a keyword-only retriever and a hybrid query.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	retriever := memory.NewRetriever(db)

	query := keywordQuery(engagementID, "postgres")
	query.Mode = memory.RetrievalHybrid

	// When: hybrid mode is requested.
	_, err := retriever.Search(t.Context(), query)

	// Then: the same typed sentinel is returned.
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrEmbeddingUnavailable)
}

func Test_Search_rejects_an_unknown_mode_when_validating(t *testing.T) {
	// Given: a query naming a mode that does not exist.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	retriever := memory.NewRetriever(db)

	query := keywordQuery(engagementID, "postgres")
	query.Mode = memory.RetrievalMode("semantic")

	// When: it is searched.
	_, err := retriever.Search(t.Context(), query)

	// Then: the mode is rejected.
	require.Error(t, err)
	assert.NotErrorIs(t, err, memory.ErrEmbeddingUnavailable,
		"an unknown mode is a different failure than a missing embedder")
}

func Test_Search_ranks_by_vector_similarity_when_an_embedder_is_supplied(t *testing.T) {
	// Given: two embedded chunks and an embedder that returns a fixed query
	// vector aligned with the second chunk.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)
	insertEmbeddedChunk(t, db, engagementID, "f-near", "postgres hardening", 5, []float32{1, 0})
	insertEmbeddedChunk(t, db, engagementID, "f-far", "postgres hardening", 5, []float32{0, 1})

	retriever := memory.NewRetrieverWithEmbedder(db, stubEmbedder{vectors: [][]float32{{1, 0}}})
	query := keywordQuery(engagementID, "irrelevant text")
	query.Mode = memory.RetrievalVector

	// When: vector mode runs.
	hits, err := retriever.Search(t.Context(), query)

	// Then: the aligned chunk ranks first with a positive score.
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "f-near", hits[0].SourceID)
	assert.InDelta(t, 1.0, hits[0].Score, 0.0001)
	assert.InDelta(t, 0.0, hits[1].Score, 0.0001)
}

func Test_RetrievalMode_Valid_accepts_every_declared_mode(t *testing.T) {
	// Given: the declared mode list.
	// When/Then: every member validates and an invented one does not.
	for _, mode := range memory.AllRetrievalModes {
		assert.True(t, mode.Valid(), "%s must be valid", mode)
	}
	assert.False(t, memory.RetrievalMode("semantic").Valid())
}

// containsSourceID reports whether any hit carries the given source id.
func containsSourceID(hits []memory.Hit, sourceID string) bool {
	for _, hit := range hits {
		if hit.SourceID == sourceID {
			return true
		}
	}
	return false
}
