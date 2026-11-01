package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cosmics/cosmics/internal/domain"
)

// storedState is the index metadata one path already has.
type storedState struct {
	id        string
	sha256    string
	enabled   bool
	chunkedAt domain.TimestampMs
}

// storedSkills reads every indexed path with the fields Sync compares.
func (l *Loader) storedSkills(ctx context.Context) (map[string]storedState, error) {
	rows, err := l.db.Reader().QueryContext(ctx,
		`SELECT path, id, sha256, enabled FROM skills ORDER BY path ASC`)
	if err != nil {
		return nil, fmt.Errorf("read indexed skills: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]storedState, 32)
	for rows.Next() {
		var (
			path, id, sha string
			enabled       int
		)
		if err := rows.Scan(&path, &id, &sha, &enabled); err != nil {
			return nil, fmt.Errorf("scan indexed skill: %w", err)
		}
		existing[path] = storedState{id: id, sha256: sha, enabled: enabled == 1}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate indexed skills: %w", err)
	}
	return existing, nil
}

// index upserts one skill and replaces its chunks in a single transaction.
func (l *Loader) index(
	ctx context.Context,
	skill Skill,
	previous storedState,
	known bool,
	now domain.TimestampMs,
) error {
	id := skill.Slug()
	if known {
		id = previous.id
	}

	stageAffinity, err := json.Marshal(skill.StageAffinity)
	if err != nil {
		return fmt.Errorf("encode stage affinity: %w", err)
	}
	tags, err := json.Marshal(skill.Tags)
	if err != nil {
		return fmt.Errorf("encode tags: %w", err)
	}

	tokenEstimate := skill.TokenEstimate
	if tokenEstimate == 0 {
		tokenEstimate = countWords(skill.Body)
	}

	chunks := skill.Chunks(defaultSkillMaxTokens)

	return l.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO skills (id, name, category, description, version, path, sha256,
			                     stage_affinity_json, tags_json, when_to_use,
			                     token_estimate, enabled, indexed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
			 ON CONFLICT (path) DO UPDATE SET
			     name                = excluded.name,
			     category            = excluded.category,
			     description         = excluded.description,
			     version              = excluded.version,
			     sha256               = excluded.sha256,
			     stage_affinity_json  = excluded.stage_affinity_json,
			     tags_json            = excluded.tags_json,
			     when_to_use          = excluded.when_to_use,
			     token_estimate       = excluded.token_estimate,
			     enabled              = 1,
			     indexed_at           = excluded.indexed_at`,
			id, skill.Name, string(skill.Category), skill.Description,
			nullText(skill.Version), skill.Path, skill.SHA256,
			string(stageAffinity), string(tags), nullText(skill.WhenToUse),
			tokenEstimate, int64(now),
		); err != nil {
			return fmt.Errorf("upsert skills row for %q: %w", skill.Path, err)
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM memory_chunks WHERE source_kind = ? AND source_id = ?`,
			sourceKindSkill, id,
		); err != nil {
			return fmt.Errorf("clear chunks for skill %q: %w", id, err)
		}

		for i, text := range chunks {
			estimate := countWords(text)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO memory_chunks
				 (id, engagement_id, source_kind, source_id, chunk_index, text,
				  token_estimate, embedding_model, embedding_dim, embedding, created_at)
				 VALUES (?, NULL, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?)`,
				l.ids.NewAt(now).String(), sourceKindSkill, id, i, text, estimate, int64(now),
			); err != nil {
				return fmt.Errorf("insert chunk %d for skill %q: %w", i, id, err)
			}
		}
		return nil
	})
}

// disableMissing flips enabled to 0 for skills whose file is gone.
//
// The rows are never deleted: memory_chunks, events, and report citations may
// reference a skill that the operator later removed from disk.
func (l *Loader) disableMissing(
	ctx context.Context,
	existing map[string]storedState,
	seen map[string]struct{},
	now domain.TimestampMs,
) (int, error) {
	missing := make([]string, 0, len(existing))
	for path, state := range existing {
		if _, present := seen[path]; present || !state.enabled {
			continue
		}
		missing = append(missing, path)
	}
	if len(missing) == 0 {
		return 0, nil
	}
	sort.Strings(missing)

	err := l.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, path := range missing {
			if _, err := tx.ExecContext(ctx,
				`UPDATE skills SET enabled = 0, indexed_at = ? WHERE path = ?`,
				int64(now), path,
			); err != nil {
				return fmt.Errorf("disable skill %q: %w", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("disable vanished skills: %w", err)
	}
	return len(missing), nil
}

// scanSkill maps one skill projection into a Skill without its body, which is
// not stored in the index.
func scanSkill(s scanner) (Skill, error) {
	var (
		skill                   Skill
		category                string
		version, whenToUse      sql.NullString
		stageAffinityJSON, tags sql.NullString
		tokenEstimate           int
	)

	if err := s.Scan(&skill.Name, &category, &version, &skill.Description,
		&whenToUse, &stageAffinityJSON, &tags, &skill.Path, &skill.SHA256,
		&tokenEstimate); err != nil {
		return Skill{}, err
	}

	skill.Category = Category(category)
	skill.Version = version.String
	skill.WhenToUse = whenToUse.String
	skill.TokenEstimate = tokenEstimate

	if stageAffinityJSON.Valid && stageAffinityJSON.String != "" {
		if err := json.Unmarshal([]byte(stageAffinityJSON.String), &skill.StageAffinity); err != nil {
			return Skill{}, fmt.Errorf("decode stage affinity for %q: %w", skill.Path, err)
		}
	}
	if tags.Valid && tags.String != "" {
		if err := json.Unmarshal([]byte(tags.String), &skill.Tags); err != nil {
			return Skill{}, fmt.Errorf("decode tags for %q: %w", skill.Path, err)
		}
	}
	return skill, nil
}

// scanner is satisfied by *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// nullText stores SQL NULL for an empty optional string.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
