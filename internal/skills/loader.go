package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// Skill source kinds and the chunk ceiling used when indexing a body.
const (
	sourceKindSkill = "skill"

	defaultSkillMaxTokens = DefaultChunkTokens
)

// Problem is one file that could not be indexed.
//
// A broken file is reported here rather than returned as an error: CORE.md
// section 8.3 requires one bad skill to be skipped without blocking startup.
type Problem struct {
	Path   string
	Reason string
}

// SyncResult counts what one Sync did.
type SyncResult struct {
	Added    int
	Updated  int
	Disabled int
	Skipped  int
	Problems []Problem
}

// Loader indexes skill files from disk into the skills and memory_chunks tables.
type Loader struct {
	db  *store.DB
	ids *domain.ULIDGenerator
	now func() domain.TimestampMs
}

// NewLoader returns a loader over db. Index times come from the system clock;
// SyncAt accepts an explicit instant for deterministic tests.
func NewLoader(db *store.DB) *Loader {
	return &Loader{db: db, ids: domain.NewULIDGenerator(), now: newTimestamp}
}

// Sync walks every .md file under root and reconciles the index with it.
//
// Files whose sha256 matches the stored value are counted as skipped and left
// untouched. Files that disappeared from disk have their skills row disabled —
// never deleted, because chunks and citations may still reference it.
func (l *Loader) Sync(ctx context.Context, root string) (SyncResult, error) {
	return l.SyncAt(ctx, root, l.now())
}

// SyncAt is Sync with an explicit indexing timestamp.
func (l *Loader) SyncAt(ctx context.Context, root string, now domain.TimestampMs) (SyncResult, error) {
	if now.IsZero() {
		return SyncResult{}, fmt.Errorf("sync skills from %q: timestamp is zero: %w", root, ErrInvalidSkill)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve skills root %q: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return SyncResult{}, fmt.Errorf("stat skills root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return SyncResult{}, fmt.Errorf("skills root %q is not a directory: %w", absRoot, ErrInvalidSkill)
	}

	paths, err := markdownFiles(absRoot)
	if err != nil {
		return SyncResult{}, fmt.Errorf("walk skills root %q: %w", absRoot, err)
	}

	existing, err := l.storedSkills(ctx)
	if err != nil {
		return SyncResult{}, err
	}

	result := SyncResult{Problems: make([]Problem, 0)}
	seen := make(map[string]struct{}, len(paths))

	for _, path := range paths {
		// A symlink escaping the root reads arbitrary files; skipping keeps the
		// index honest about what it actually indexed.
		escaped, err := escapesRoot(absRoot, path)
		if err != nil {
			result.Problems = append(result.Problems, Problem{Path: path, Reason: err.Error()})
			continue
		}
		if escaped {
			result.Problems = append(result.Problems, Problem{
				Path: path, Reason: "resolves outside the skills root",
			})
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			result.Problems = append(result.Problems, Problem{Path: path, Reason: err.Error()})
			continue
		}
		skill, err := ParseSkill(path, raw)
		if err != nil {
			result.Problems = append(result.Problems, Problem{Path: path, Reason: err.Error()})
			continue
		}

		seen[path] = struct{}{}
		previous, known := existing[path]
		if known && previous.sha256 == skill.SHA256 && previous.enabled {
			result.Skipped++
			continue
		}

		if err := l.index(ctx, skill, previous, known, now); err != nil {
			return SyncResult{}, fmt.Errorf("index skill %q: %w", path, err)
		}
		if known {
			result.Updated++
		} else {
			result.Added++
		}
	}

	disabled, err := l.disableMissing(ctx, existing, seen, now)
	if err != nil {
		return SyncResult{}, err
	}
	result.Disabled = disabled

	sort.Slice(result.Problems, func(i, j int) bool {
		return result.Problems[i].Path < result.Problems[j].Path
	})
	return result, nil
}

// List returns indexed skills in path order.
func (l *Loader) List(ctx context.Context, onlyEnabled bool) ([]Skill, error) {
	query := `SELECT name, category, version, description, when_to_use,
	                 stage_affinity_json, tags_json, path, sha256, token_estimate
	            FROM skills`
	if onlyEnabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY path ASC`

	rows, err := l.db.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	list := make([]Skill, 0, 16)
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		list = append(list, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skills: %w", err)
	}
	return list, nil
}

// Get returns one indexed skill by slug.
func (l *Loader) Get(ctx context.Context, slug string) (Skill, error) {
	rows, err := l.db.Reader().QueryContext(ctx,
		`SELECT name, category, version, description, when_to_use,
		        stage_affinity_json, tags_json, path, sha256, token_estimate
		   FROM skills WHERE id = ?`, slug)
	if err != nil {
		return Skill{}, fmt.Errorf("get skill %q: %w", slug, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Skill{}, fmt.Errorf("get skill %q: %w", slug, err)
		}
		return Skill{}, fmt.Errorf("get skill %q: %w", slug, ErrNotFound)
	}
	skill, err := scanSkill(rows)
	if err != nil {
		return Skill{}, fmt.Errorf("get skill %q: %w", slug, err)
	}
	return skill, nil
}
