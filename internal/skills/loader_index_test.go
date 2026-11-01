package skills_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/skills"
)

func Test_Get_returns_the_indexed_skill_when_it_exists(t *testing.T) {
	// Given: a synced skill.
	db := openTestDB(t)
	root := t.TempDir()
	writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map it.\n"))

	loader := skills.NewLoader(db)
	_, err := loader.SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)

	// When: it is fetched by name and by slug.
	byName, err := loader.List(t.Context(), true)
	require.NoError(t, err)
	require.Len(t, byName, 1)

	got, err := loader.Get(t.Context(), "auth-testing")

	// Then: the row round-trips, including the JSON-decoded lists.
	require.NoError(t, err)
	assert.Equal(t, "Auth Testing", got.Name)
	assert.Equal(t, skills.CategoryWeb, got.Category)
	assert.Equal(t, []string{"recon", "analysis"}, stageStrings(got))
	assert.Equal(t, "1.0.0", got.Version)
}

func Test_Get_returns_ErrNotFound_when_the_skill_is_absent(t *testing.T) {
	// Given: an empty index.
	db := openTestDB(t)

	// When: an unknown slug is requested.
	_, err := skills.NewLoader(db).Get(t.Context(), "no-such-skill")

	// Then: the typed sentinel is returned.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrNotFound)
}

func Test_DefaultFiles_parses_every_embedded_skill_when_validated(t *testing.T) {
	// Given: the embedded default skill set.
	files, err := skills.DefaultSet()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 6, "the binary must ship at least six defaults")

	slugs := make(map[string]string, len(files))

	// When: each file is parsed and validated exactly as the loader would.
	for name, raw := range files {
		skill, err := skills.ParseSkill(name, raw)
		require.NoError(t, err, "embedded default %q must parse", name)

		// Then: the shipped content satisfies the front-matter contract.
		assert.NotEmpty(t, skill.Name, "%s has no name", name)
		assert.True(t, skill.Category.Valid(), "%s has category %q", name, skill.Category)
		assert.NotEmpty(t, skill.Description, "%s has no description", name)
		assert.NotEmpty(t, skill.WhenToUse, "%s has no when_to_use", name)
		assert.NotEmpty(t, skill.StageAffinity, "%s has no stage_affinity", name)
		assert.NotEmpty(t, strings.TrimSpace(skill.Body), "%s has an empty body", name)
		assert.Len(t, skill.SHA256, 64)

		assert.NotContains(t, strings.ToLower(skill.Body), "lorem ipsum", "%s is placeholder text", name)
		for _, heading := range []string{"## Preconditions", "## Procedure", "## Signals"} {
			assert.Contains(t, skill.Body, heading, "%s is missing %s", name, heading)
		}

		slug := skill.Slug()
		previous, duplicate := slugs[slug]
		assert.False(t, duplicate, "%s and %s share the slug %q", name, previous, slug)
		slugs[slug] = name
	}
}

func Test_DefaultFiles_covers_the_documented_categories_when_shipped(t *testing.T) {
	// Given: the embedded defaults.
	files, err := skills.DefaultSet()
	require.NoError(t, err)

	// When: their categories are collected.
	seen := make(map[skills.Category]bool, len(files))
	for name, raw := range files {
		skill, err := skills.ParseSkill(name, raw)
		require.NoError(t, err)
		seen[skill.Category] = true
	}

	// Then: the shipped set spans the core categories the pipeline needs.
	for _, category := range []skills.Category{
		skills.CategoryRecon, skills.CategoryWeb, skills.CategoryNetwork,
		skills.CategoryExploit, skills.CategoryReport, skills.CategoryMeta,
	} {
		assert.True(t, seen[category], "no embedded skill covers category %s", category)
	}
}

func Test_DefaultFiles_returns_a_copy_when_called_twice(t *testing.T) {
	// Given: two reads of the embedded set.
	first := skills.DefaultFiles()
	second := skills.DefaultFiles()

	// When: the first result is mutated.
	for name := range first {
		first[name] = []byte("mutated")
	}

	// Then: the second read is unaffected, so no caller can corrupt the shipped set.
	for name, raw := range second {
		assert.NotEqual(t, "mutated", string(raw), "%s leaked a shared slice", name)
	}
}

func Test_DefaultNames_is_sorted_and_matches_the_file_count(t *testing.T) {
	// Given: the embedded defaults.
	files := skills.DefaultFiles()
	names := skills.DefaultNames()

	// When/Then: the names are sorted and complete.
	assert.Len(t, names, len(files))
	assert.True(t, sort.StringsAreSorted(names))
	for _, name := range names {
		assert.Contains(t, files, name)
	}
}

// stageStrings renders the stage affinity list for comparison.
func stageStrings(skill skills.Skill) []string {
	out := make([]string, 0, len(skill.StageAffinity))
	for _, stage := range skill.StageAffinity {
		out = append(out, string(stage))
	}
	return out
}
