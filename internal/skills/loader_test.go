package skills_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/skills"
	"github.com/cosmics/cosmics/internal/store"
)

// baseTimestamp is a fixed instant so indexed_at assertions stay deterministic.
const baseTimestamp = 1_760_000_000_000

// openTestDB opens a migrated database under t.TempDir and registers its close.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "cosmics.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

// skillFile renders a valid skill markdown document for one name and category.
func skillFile(name, category, body string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"category: " + category + "\n" +
		"version: 1.0.0\n" +
		"description: A skill about " + strings.ToLower(name) + "\n" +
		"when_to_use: The engagement reaches the " + category + " stage\n" +
		"stage_affinity: [recon, analysis]\n" +
		"tags: [fixture]\n" +
		"---\n\n" + body
}

// writeSkill writes one markdown file under root.
func writeSkill(t *testing.T, root, filename, content string) string {
	t.Helper()

	path := filepath.Join(root, filename)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// countRows returns the number of rows a scalar query produces.
func countRows(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()

	var count int
	require.NoError(t, db.Reader().QueryRowContext(t.Context(), query, args...).Scan(&count))
	return count
}

func Test_Sync_adds_skills_when_the_root_contains_markdown(t *testing.T) {
	// Given: a root with two valid skills in nested directories.
	db := openTestDB(t)
	root := t.TempDir()
	writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map the login form.\n"))
	writeSkill(t, root, "recon/subs.md", skillFile("Subdomain Enum", "recon", "## Procedure\n\n1. Collect candidates.\n"))

	loader := skills.NewLoader(db)

	// When: the root is synced.
	result, err := loader.SyncAt(t.Context(), root, baseTimestamp)

	// Then: both are added, indexed, and chunked.
	require.NoError(t, err)
	assert.Equal(t, 2, result.Added)
	assert.Zero(t, result.Updated)
	assert.Zero(t, result.Disabled)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Problems)

	assert.Equal(t, 2, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE enabled = 1`))
	assert.Equal(t, 2, countRows(t, db, `SELECT COUNT(*) FROM memory_chunks WHERE source_kind = 'skill'`))

	indexed, err := loader.List(t.Context(), true)
	require.NoError(t, err)
	require.Len(t, indexed, 2)
	assert.Equal(t, "Subdomain Enum", indexed[0].Name, "List orders by path")
	assert.Equal(t, skills.CategoryRecon, indexed[0].Category)
	assert.Len(t, indexed[0].StageAffinity, 2)
	assert.Equal(t, []string{"fixture"}, indexed[0].Tags)
}

func Test_Sync_indexes_a_global_chunk_row_when_a_skill_is_chunked(t *testing.T) {
	// Given: a skill whose body has two H2 sections.
	db := openTestDB(t)
	root := t.TempDir()
	body := "## First\n\n" + strings.TrimSpace(strings.Repeat("alpha ", 500)) +
		"\n\n## Second\n\n" + strings.TrimSpace(strings.Repeat("beta ", 500)) + "\n"
	writeSkill(t, root, "big.md", skillFile("Big Skill", "meta", body))

	// When: it is synced.
	_, err := skills.NewLoader(db).SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)

	// Then: more than one chunk is stored and every skill chunk is global.
	chunks := countRows(t, db, `SELECT COUNT(*) FROM memory_chunks WHERE source_kind = 'skill'`)
	assert.Greater(t, chunks, 1, "a body over the ceiling must be split")

	scoped := countRows(t, db,
		`SELECT COUNT(*) FROM memory_chunks WHERE source_kind = 'skill' AND engagement_id IS NOT NULL`)
	assert.Zero(t, scoped, "skills are global memory and carry no engagement")

	empties := countRows(t, db, `SELECT COUNT(*) FROM memory_chunks WHERE trim(text) = ''`)
	assert.Zero(t, empties, "a blank chunk must never be indexed")
}

func Test_Sync_skips_unchanged_files_when_run_twice(t *testing.T) {
	// Given: a root synced once.
	db := openTestDB(t)
	root := t.TempDir()
	writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map the login form.\n"))
	writeSkill(t, root, "recon/subs.md", skillFile("Subdomain Enum", "recon", "## Procedure\n\n1. Collect candidates.\n"))

	loader := skills.NewLoader(db)
	first, err := loader.SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)
	require.Equal(t, 2, first.Added)

	// When: the same root is synced again with no file changes.
	second, err := loader.SyncAt(t.Context(), root, baseTimestamp+1_000)

	// Then: nothing is added or updated; both are reported as skipped and the
	// stored index timestamps did not move.
	require.NoError(t, err)
	assert.Zero(t, second.Added)
	assert.Zero(t, second.Updated)
	assert.Zero(t, second.Disabled)
	assert.Equal(t, 2, second.Skipped)

	assert.Equal(t, 2, countRows(t, db,
		`SELECT COUNT(*) FROM skills WHERE indexed_at = ?`, int64(baseTimestamp)))
}

func Test_Sync_updates_a_skill_when_its_content_changes(t *testing.T) {
	// Given: a synced root.
	db := openTestDB(t)
	root := t.TempDir()
	path := writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map the login form.\n"))

	loader := skills.NewLoader(db)
	_, err := loader.SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)

	var originalSHA string
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT sha256 FROM skills WHERE path = ?`, path).Scan(&originalSHA))

	// When: the body changes and the root is resynced.
	writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map the login form.\n2. Test MFA bypass.\n"))
	result, err := loader.SyncAt(t.Context(), root, baseTimestamp+1_000)

	// Then: it counts as updated, the hash moved, and chunks were replaced.
	require.NoError(t, err)
	assert.Equal(t, 1, result.Updated)
	assert.Zero(t, result.Added)

	var updatedSHA string
	require.NoError(t, db.Reader().QueryRowContext(t.Context(),
		`SELECT sha256 FROM skills WHERE path = ?`, path).Scan(&updatedSHA))
	assert.NotEqual(t, originalSHA, updatedSHA)
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE path = ?`, path))
}

func Test_Sync_disables_a_skill_when_its_file_is_removed(t *testing.T) {
	// Given: a root with two skills synced.
	db := openTestDB(t)
	root := t.TempDir()
	keptPath := writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map it.\n"))
	removedPath := writeSkill(t, root, "recon/subs.md", skillFile("Subdomain Enum", "recon", "## Procedure\n\n1. Collect.\n"))

	loader := skills.NewLoader(db)
	_, err := loader.SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)
	require.Equal(t, 2, countRows(t, db, `SELECT COUNT(*) FROM skills`))

	// When: one file is deleted from disk and the root is resynced.
	require.NoError(t, os.Remove(removedPath))
	result, err := loader.SyncAt(t.Context(), root, baseTimestamp+1_000)

	// Then: the row survives with enabled=0 rather than being deleted.
	require.NoError(t, err)
	assert.Equal(t, 1, result.Disabled)
	assert.Zero(t, result.Added)

	assert.Equal(t, 2, countRows(t, db, `SELECT COUNT(*) FROM skills`),
		"a vanished file must never delete its row")
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE path = ? AND enabled = 0`, removedPath))
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE path = ? AND enabled = 1`, keptPath))

	enabled, err := loader.List(t.Context(), true)
	require.NoError(t, err)
	require.Len(t, enabled, 1)
	assert.Equal(t, "Auth Testing", enabled[0].Name)

	all, err := loader.List(t.Context(), false)
	require.NoError(t, err)
	assert.Len(t, all, 2, "the disabled row is still listed when asked for everything")
}

func Test_Sync_reactivates_a_skill_when_its_file_returns(t *testing.T) {
	// Given: a skill that was disabled after its file disappeared.
	db := openTestDB(t)
	root := t.TempDir()
	path := writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map it.\n"))

	loader := skills.NewLoader(db)
	_, err := loader.SyncAt(t.Context(), root, baseTimestamp)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	_, err = loader.SyncAt(t.Context(), root, baseTimestamp+1_000)
	require.NoError(t, err)
	require.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE enabled = 0`))

	// When: the same content is restored and the root is resynced.
	writeSkill(t, root, "web/auth.md", skillFile("Auth Testing", "web", "## Procedure\n\n1. Map it.\n"))
	result, err := loader.SyncAt(t.Context(), root, baseTimestamp+2_000)

	// Then: it is re-enabled through the upsert path.
	require.NoError(t, err)
	assert.Equal(t, 1, result.Updated)
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills WHERE path = ? AND enabled = 1`, path))
}

func Test_Sync_reports_problems_without_failing_when_one_file_is_invalid(t *testing.T) {
	// Given: a root with one valid skill and two broken files.
	db := openTestDB(t)
	root := t.TempDir()
	writeSkill(t, root, "good.md", skillFile("Good Skill", "meta", "## Procedure\n\n1. Do the thing.\n"))
	writeSkill(t, root, "no-front-matter.md", "# No front matter\n\nJust prose.\n")
	writeSkill(t, root, "bad-yaml.md", "---\nname: [unclosed\ncategory: web\n---\n\nBody.\n")

	loader := skills.NewLoader(db)

	// When: the root is synced.
	result, err := loader.SyncAt(t.Context(), root, baseTimestamp)

	// Then: the valid file is indexed, the bad ones are reported, and no error
	// is returned.
	require.NoError(t, err, "one bad file must not block startup")
	assert.Equal(t, 1, result.Added)
	require.Len(t, result.Problems, 2)

	reported := make([]string, 0, len(result.Problems))
	for _, problem := range result.Problems {
		assert.NotEmpty(t, problem.Reason, "every problem names why the file was skipped")
		reported = append(reported, filepath.Base(problem.Path))
	}
	sort.Strings(reported)
	assert.Equal(t, []string{"bad-yaml.md", "no-front-matter.md"}, reported)

	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills`))
}

func Test_Sync_sorts_problems_when_several_files_are_invalid(t *testing.T) {
	// Given: three invalid files written in a non-lexical order.
	db := openTestDB(t)
	root := t.TempDir()
	for _, name := range []string{"zulu.md", "alpha.md", "mike.md"} {
		writeSkill(t, root, name, "# not a skill\n")
	}

	// When: the root is synced.
	result, err := skills.NewLoader(db).SyncAt(t.Context(), root, baseTimestamp)

	// Then: problems are reported in a stable path order.
	require.NoError(t, err)
	require.Len(t, result.Problems, 3)
	assert.Less(t, result.Problems[0].Path, result.Problems[1].Path)
	assert.Less(t, result.Problems[1].Path, result.Problems[2].Path)
}

func Test_Sync_ignores_non_markdown_files_when_walking(t *testing.T) {
	// Given: a root holding markdown plus other file types.
	db := openTestDB(t)
	root := t.TempDir()
	writeSkill(t, root, "skill.md", skillFile("Real Skill", "meta", "## Procedure\n\n1. Go.\n"))
	writeSkill(t, root, "notes.txt", "not a skill")
	writeSkill(t, root, "data.json", "{}")

	// When: the root is synced.
	result, err := skills.NewLoader(db).SyncAt(t.Context(), root, baseTimestamp)

	// Then: only markdown is considered.
	require.NoError(t, err)
	assert.Equal(t, 1, result.Added)
	assert.Empty(t, result.Problems)
	assert.Equal(t, 1, countRows(t, db, `SELECT COUNT(*) FROM skills`))
}

func Test_Sync_returns_an_error_when_the_root_does_not_exist(t *testing.T) {
	// Given: a path that is not present on disk.
	db := openTestDB(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	// When: it is synced.
	_, err := skills.NewLoader(db).SyncAt(t.Context(), missing, baseTimestamp)

	// Then: the missing root is an error, not an empty success.
	require.Error(t, err)
}

func Test_Sync_returns_an_error_when_the_root_is_a_file(t *testing.T) {
	// Given: a path that names a regular file.
	db := openTestDB(t)
	root := t.TempDir()
	path := writeSkill(t, root, "single.md", skillFile("Single", "meta", "body"))

	// When: it is synced as if it were a directory.
	_, err := skills.NewLoader(db).SyncAt(t.Context(), path, baseTimestamp)

	// Then: it is rejected.
	require.Error(t, err)
	assert.ErrorIs(t, err, skills.ErrInvalidSkill)
}
