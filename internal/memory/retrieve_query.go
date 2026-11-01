package memory

import (
	"sort"
	"strings"
	"unicode"
)

// defaultTopK bounds a query that did not set TopK.
const defaultTopK = 10

// candidateLimit is the FTS5 candidate window the pipeline re-ranks.
const candidateLimit = 200

// rrfK is the reciprocal-rank-fusion constant from ARCHITECTURE section 8.2.
const rrfK = 60

// scopeClause builds the engagement and source-kind filter shared by both
// candidate paths. Global rows (NULL engagement_id) are always eligible, because
// skills and lessons are stored without an engagement.
func scopeClause(q Query) (string, []any) {
	parts := make([]string, 0, 2)
	args := make([]any, 0, len(q.Kinds)+1)

	if !q.EngagementID.IsZero() {
		parts = append(parts, "(c.engagement_id = ? OR c.engagement_id IS NULL)")
		args = append(args, q.EngagementID.String())
	} else {
		parts = append(parts, "c.engagement_id IS NULL")
	}

	if len(q.Kinds) > 0 {
		parts = append(parts, "c.source_kind IN ("+placeholders(len(q.Kinds))+")")
		for _, kind := range q.Kinds {
			args = append(args, kind)
		}
	}
	return strings.Join(parts, " AND "), args
}

// placeholders returns n comma-separated bind markers.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// applyBudget projects hits under the token budget and TopK cap.
//
// Accumulation stops at the first hit that would overflow the budget rather than
// skipping it: the list is relevance-ordered, so a smaller later hit filling the
// gap would put a weaker match ahead of the ordering contract.
func applyBudget(hits []Hit, q Query) []Hit {
	topK := q.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	out := make([]Hit, 0, min(topK, len(hits)))
	used := 0
	for _, hit := range hits {
		if len(out) >= topK {
			break
		}
		if q.TokenBudget > 0 && used+hit.Tokens > q.TokenBudget {
			break
		}
		used += hit.Tokens
		out = append(out, hit)
	}
	return out
}

// hitsFrom projects scored chunks, already ordered best-first, into hits.
func hitsFrom(chunks []chunk) []Hit {
	hits := make([]Hit, 0, len(chunks))
	for _, c := range chunks {
		hits = append(hits, Hit{
			SourceKind: c.sourceKind,
			SourceID:   c.sourceID,
			Text:       c.text,
			Score:      c.score,
			Tokens:     c.tokens,
		})
	}
	return hits
}

// sortChunks orders by score descending, then by identity so equal scores never
// reorder between runs.
func sortChunks(chunks []chunk) {
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].score != chunks[j].score {
			return chunks[i].score > chunks[j].score
		}
		if chunks[i].sourceKind != chunks[j].sourceKind {
			return chunks[i].sourceKind < chunks[j].sourceKind
		}
		if chunks[i].sourceID != chunks[j].sourceID {
			return chunks[i].sourceID < chunks[j].sourceID
		}
		return chunks[i].text < chunks[j].text
	})
}

// ftsQuery turns free text into a safe FTS5 MATCH expression.
//
// Raw user text is not a valid MATCH expression: punctuation, unbalanced quotes,
// and FTS5 operators all either error or change meaning. Tokens are reduced to
// letters and digits, quoted, and OR-ed so bm25 can rank partial matches.
func ftsQuery(text string) string {
	const maxTerms = 24

	seen := make(map[string]struct{}, maxTerms)
	terms := make([]string, 0, maxTerms)
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		term := strings.ToLower(field)
		if _, duplicate := seen[term]; duplicate {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, `"`+term+`"`)
		if len(terms) == maxTerms {
			break
		}
	}
	return strings.Join(terms, " OR ")
}
