package skills_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/skills"
)

// validSkill is a minimal file that satisfies every required front-matter field.
const validSkill = `---
name: Web Authentication Testing
category: web
version: 1.0.0
description: Systematic testing of login and session flows
when_to_use: The target exposes a login form or a session cookie
stage_affinity: [analysis, exploit]
tags: [auth, session, jwt]
requires_tools: [ffuf, hydra]
token_estimate: 2400
---

# Web Authentication Testing

## Preconditions

An account exists and is authorized for testing.

## Procedure

1. Map the entry points.
2. Test the session lifecycle.

## Signals

A cookie that never rotates at login.
`

func Test_ParseSkill_extracts_front_matter_and_body_when_input_is_valid(t *testing.T) {
	// Given: a well-formed skill file.
	// When: it is parsed.
	skill, err := skills.ParseSkill("/skills/web-authentication.md", []byte(validSkill))

	// Then: every front-matter field and the body are extracted.
	require.NoError(t, err)
	assert.Equal(t, "Web Authentication Testing", skill.Name)
	assert.Equal(t, skills.CategoryWeb, skill.Category)
	assert.Equal(t, "1.0.0", skill.Version)
	assert.Equal(t, "Systematic testing of login and session flows", skill.Description)
	assert.Equal(t, "The target exposes a login form or a session cookie", skill.WhenToUse)
	assert.Equal(t, []string{"auth", "session", "jwt"}, skill.Tags)
	assert.Equal(t, []string{"ffuf", "hydra"}, skill.RequiresTools)
	assert.Equal(t, 2400, skill.TokenEstimate)
	assert.Len(t, skill.StageAffinity, 2)
	assert.Equal(t, "/skills/web-authentication.md", skill.Path)

	assert.True(t, strings.HasPrefix(skill.Body, "# Web Authentication Testing"),
		"the body starts after the closing delimiter, with no leading blank line")
	assert.NotContains(t, skill.Body, "---", "the front matter must not leak into the body")
	assert.Contains(t, skill.Body, "## Procedure")
}

func Test_ParseSkill_tolerates_a_UTF8_BOM_when_the_file_starts_with_one(t *testing.T) {
	// Given: the same valid file prefixed with a BOM.
	raw := append([]byte("\ufeff"), []byte(validSkill)...)

	// When: it is parsed.
	skill, err := skills.ParseSkill("bom.md", raw)

	// Then: the BOM does not break delimiter detection.
	require.NoError(t, err)
	assert.Equal(t, "Web Authentication Testing", skill.Name)
}

func Test_ParseSkill_returns_an_error_when_the_front_matter_is_missing(t *testing.T) {
	// Given: a markdown body with no front matter.
	raw := []byte("# Just a body\n\nNo YAML block at all.\n")

	// When: it is parsed.
	_, err := skills.ParseSkill("no-front-matter.md", raw)

	// Then: the typed sentinel is returned.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrInvalidSkill)
}

func Test_ParseSkill_returns_an_error_when_the_front_matter_is_unterminated(t *testing.T) {
	// Given: an opening delimiter with no closing one.
	raw := []byte("---\nname: Broken\ncategory: web\n")

	// When: it is parsed.
	_, err := skills.ParseSkill("unterminated.md", raw)

	// Then: the sentinel is returned rather than a panic or a silent empty parse.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrInvalidSkill)
}

func Test_ParseSkill_returns_an_error_when_a_required_field_is_blank(t *testing.T) {
	// Given: one table row per required field, each blanked in turn.
	const (
		blankName        = "name: \"\""
		blankCategory    = "category: \"\""
		blankDescription = "description: \"\""
		blankWhenToUse   = "when_to_use: \"\""
		blankStageAffin  = "stage_affinity: []"
	)

	tests := []struct {
		name     string
		from, to string
	}{
		{"name", "name: Web Authentication Testing", blankName},
		{"category", "category: web", blankCategory},
		{"description", "description: Systematic testing of login and session flows", blankDescription},
		{"when_to_use", "when_to_use: The target exposes a login form or a session cookie", blankWhenToUse},
		{"stage_affinity", "stage_affinity: [analysis, exploit]", blankStageAffin},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(strings.Replace(validSkill, tc.from, tc.to, 1))
			require.NotEqual(t, validSkill, string(raw), "the fixture must actually change")

			// When: the broken file is parsed.
			_, err := skills.ParseSkill(tc.name+".md", raw)

			// Then: it is rejected with the typed sentinel naming the field.
			require.Error(t, err)
			assert.ErrorIs(t, err, skills.ErrInvalidSkill)
			assert.Contains(t, err.Error(), tc.name)
		})
	}
}

func Test_ParseSkill_returns_an_error_when_the_category_is_not_a_declared_value(t *testing.T) {
	// Given: a file claiming a category outside the CHECK constraint.
	raw := []byte(strings.Replace(validSkill, "category: web", "category: quantum", 1))

	// When: it is parsed.
	_, err := skills.ParseSkill("bad-category.md", raw)

	// Then: it is rejected.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrInvalidSkill)
}

func Test_ParseSkill_returns_an_error_when_a_stage_affinity_entry_is_unknown(t *testing.T) {
	// Given: a file whose stage affinity names a stage that does not exist.
	raw := []byte(strings.Replace(validSkill, "[analysis, exploit]", "[analysis, telepathy]", 1))

	// When: it is parsed.
	_, err := skills.ParseSkill("bad-stage.md", raw)

	// Then: it is rejected.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrInvalidSkill)
}

func Test_ParseSkill_computes_a_stable_sha256_when_the_bytes_are_identical(t *testing.T) {
	// Given: the same bytes parsed twice under different paths.
	first, err := skills.ParseSkill("a/web-auth.md", []byte(validSkill))
	require.NoError(t, err)
	second, err := skills.ParseSkill("b/copy.md", []byte(validSkill))
	require.NoError(t, err)

	// When/Then: the content hash is identical, because it covers the bytes only.
	assert.Equal(t, first.SHA256, second.SHA256)
	assert.Len(t, first.SHA256, 64, "sha256 renders as 64 lowercase hex characters")
	assert.NotEqual(t, first.Path, second.Path)
}

func Test_ParseSkill_changes_the_sha256_when_only_the_body_changes(t *testing.T) {
	// Given: two files differing only inside the body.
	base, err := skills.ParseSkill("skill.md", []byte(validSkill))
	require.NoError(t, err)

	edited := strings.Replace(validSkill, "A cookie that never rotates at login.", "A cookie that rotates at login.", 1)
	changed, err := skills.ParseSkill("skill.md", []byte(edited))
	require.NoError(t, err)

	// When/Then: the hash differs, which is what triggers a reindex.
	assert.NotEqual(t, base.SHA256, changed.SHA256)
}

func Test_Chunks_splits_on_h2_headings_when_the_body_has_sections(t *testing.T) {
	// Given: a skill whose body has three H2 sections, each small.
	body := strings.Join([]string{
		"# Title",
		"",
		"## Preconditions",
		"",
		"One short paragraph here.",
		"",
		"## Procedure",
		"",
		"Another short paragraph here.",
		"",
		"## Signals",
		"",
		"A third short paragraph here.",
	}, "\n")

	skill := mustSkill(t, body)

	// When: the body is chunked with a ceiling too small to hold two sections.
	chunks := skill.Chunks(6)

	// Then: each chunk begins at a section boundary and they cover the body.
	require.NotEmpty(t, chunks)
	for i, chunk := range chunks {
		assert.NotEmpty(t, strings.TrimSpace(chunk), "chunk %d must not be blank", i)
		assert.LessOrEqual(t, wordCount(chunk), 6, "chunk %d exceeds the ceiling", i)
	}

	joined := strings.Join(chunks, "\n")
	assert.Contains(t, joined, "Preconditions")
	assert.Contains(t, joined, "Procedure")
	assert.Contains(t, joined, "Signals")
}

func Test_Chunks_joins_small_sections_when_they_fit_under_the_ceiling(t *testing.T) {
	// Given: three tiny sections and a generous ceiling.
	body := "## A\n\nalpha beta\n\n## B\n\ngamma delta\n\n## C\n\nepsilon zeta\n"
	skill := mustSkill(t, body)

	// When: the body is chunked with room for all three.
	chunks := skill.Chunks(100)

	// Then: they are joined into one chunk rather than fragmented.
	require.Len(t, chunks, 1)
	assert.Contains(t, chunks[0], "alpha")
	assert.Contains(t, chunks[0], "epsilon")
}

func Test_Chunks_bounds_chunk_size_when_a_section_is_oversized(t *testing.T) {
	// Given: one H2 section holding far more words than the ceiling allows.
	paragraph := strings.TrimSpace(strings.Repeat("word ", 400))
	body := "## Huge\n\n" + paragraph + "\n"
	skill := mustSkill(t, body)

	// When: the body is chunked with a 50-word ceiling.
	chunks := skill.Chunks(50)

	// Then: no chunk exceeds the ceiling, no word is lost, and the heading lands
	// on the first piece only.
	require.Greater(t, len(chunks), 1, "an oversized section must be split")
	for i, chunk := range chunks {
		assert.LessOrEqual(t, wordCount(chunk), 50, "chunk %d exceeds the ceiling", i)
	}

	assert.True(t, strings.HasPrefix(chunks[0], "## Huge"), "the heading opens the first chunk")
	for _, chunk := range chunks[1:] {
		assert.NotContains(t, chunk, "## Huge", "the heading is not repeated")
	}

	bodyWords := 0
	for _, chunk := range chunks {
		bodyWords += wordCount(strings.Replace(chunk, "## Huge", "", 1))
	}
	assert.Equal(t, 400, bodyWords, "every body word survives the split exactly once")
}

func Test_Chunks_returns_the_preamble_when_the_body_has_no_h2(t *testing.T) {
	// Given: a body with a single H1 and no H2 sections.
	skill := mustSkill(t, "# Title\n\nA short body with no second-level heading.\n")

	// When: it is chunked.
	chunks := skill.Chunks(100)

	// Then: the whole body is one chunk.
	require.Len(t, chunks, 1)
	assert.Contains(t, chunks[0], "A short body")
}

func Test_Chunks_returns_nothing_when_the_body_is_empty(t *testing.T) {
	// Given: an empty body.
	skill := mustSkill(t, "")

	// When/Then: chunking yields no chunks instead of one blank chunk.
	assert.Empty(t, skill.Chunks(100))
}

func Test_Chunks_uses_the_default_ceiling_when_maxTokens_is_not_positive(t *testing.T) {
	// Given: a body with an oversized section and no explicit ceiling.
	body := "## Huge\n\n" + strings.TrimSpace(strings.Repeat("word ", 2000)) + "\n"
	skill := mustSkill(t, body)

	// When: chunked with zero.
	chunks := skill.Chunks(0)

	// Then: the documented default of 800 applies.
	require.NotEmpty(t, chunks)
	for i, chunk := range chunks {
		assert.LessOrEqual(t, wordCount(chunk), skills.DefaultChunkTokens, "chunk %d exceeds the default", i)
	}
}

func Test_Category_Valid_accepts_every_declared_value(t *testing.T) {
	// Given: the declared category list.
	// When/Then: every member validates, and an invented category does not.
	for _, category := range skills.AllCategories {
		assert.True(t, category.Valid(), "%s must be valid", category)
	}
	assert.False(t, skills.Category("quantum").Valid())
}

func Test_Slug_derives_a_stable_identifier_when_the_name_has_punctuation(t *testing.T) {
	// Given: a skill whose display name contains spaces and punctuation.
	skill := mustSkill(t, "body")
	skill.Name = "Web Authentication: Testing & Hardening!"

	// When/Then: the slug is lowercase, dash-separated, and punctuation-free.
	assert.Equal(t, "web-authentication-testing-hardening", skill.Slug())
}

// mustSkill builds a parsed skill around an arbitrary body.
func mustSkill(t *testing.T, body string) skills.Skill {
	t.Helper()

	raw := "---\n" +
		"name: Fixture Skill\n" +
		"category: meta\n" +
		"description: A fixture used by the skill tests\n" +
		"when_to_use: A test needs a parsed skill value\n" +
		"stage_affinity: [recon]\n" +
		"---\n\n" + body

	skill, err := skills.ParseSkill("fixture.md", []byte(raw))
	require.NoError(t, err)
	return skill
}

// wordCount counts whitespace-separated words, matching the package estimate.
func wordCount(text string) int {
	return len(strings.Fields(text))
}
