// Package skills discovers, parses, validates, hashes, and chunks markdown
// playbooks. The files on disk are the source of truth; the skills table is a
// searchable index of them (docs/ERD.md section 3.20).
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors. Callers branch with errors.Is.
var (
	// ErrInvalidSkill is returned when a skill file cannot be parsed or violates
	// the front-matter contract in docs/CORE.md section 8.2.
	ErrInvalidSkill = errors.New("skills: invalid skill")

	// ErrNotFound is returned when a requested skill is not indexed.
	ErrNotFound = errors.New("skills: not found")
)

// Category is one of the ten skill categories in the skills.category CHECK list.
type Category string

// Skill categories. These strings are persisted and are never renamed.
const (
	CategoryRecon   Category = "recon"
	CategoryOSINT   Category = "osint"
	CategoryWeb     Category = "web"
	CategoryNetwork Category = "network"
	CategoryCloud   Category = "cloud"
	CategoryMobile  Category = "mobile"
	CategoryExploit Category = "exploit"
	CategoryPost    Category = "post"
	CategoryReport  Category = "report"
	CategoryMeta    Category = "meta"
)

// AllCategories lists every declared category, for exhaustiveness tests.
var AllCategories = []Category{
	CategoryRecon, CategoryOSINT, CategoryWeb, CategoryNetwork, CategoryCloud,
	CategoryMobile, CategoryExploit, CategoryPost, CategoryReport, CategoryMeta,
}

// Valid reports whether the category is one of the declared constants.
func (c Category) Valid() bool {
	switch c {
	case CategoryRecon, CategoryOSINT, CategoryWeb, CategoryNetwork, CategoryCloud,
		CategoryMobile, CategoryExploit, CategoryPost, CategoryReport, CategoryMeta:
		return true
	}
	return false
}

// FrontMatter is the YAML block delimited by leading and closing "---" lines.
type FrontMatter struct {
	Name          string             `yaml:"name"`
	Category      Category           `yaml:"category"`
	Version       string             `yaml:"version"`
	Description   string             `yaml:"description"`
	WhenToUse     string             `yaml:"when_to_use"`
	StageAffinity []domain.StageKind `yaml:"stage_affinity"`
	Tags          []string           `yaml:"tags"`
	RequiresTools []string           `yaml:"requires_tools"`
	TokenEstimate int                `yaml:"token_estimate"`
}

// Skill is one parsed playbook: its validated front matter, its path, the
// content hash that decides reindexing, and its markdown body.
type Skill struct {
	FrontMatter
	Path   string
	SHA256 string
	Body   string
}

// frontMatterDelimiter opens and closes the YAML block.
const frontMatterDelimiter = "---"

// ParseSkill splits, validates, and hashes one skill file.
//
// The hash covers the raw bytes, not the parsed fields, so any edit — including
// a body-only edit — triggers a reindex. It is a parse-time error, not a lenient
// skip: the loader decides what to do with a broken file.
func ParseSkill(path string, raw []byte) (Skill, error) {
	if strings.TrimSpace(path) == "" {
		return Skill{}, fmt.Errorf("parse skill: path is empty: %w", ErrInvalidSkill)
	}
	if !utf8.Valid(raw) {
		return Skill{}, fmt.Errorf("parse skill %q: not valid UTF-8: %w", path, ErrInvalidSkill)
	}

	front, body, err := splitFrontMatter(raw)
	if err != nil {
		return Skill{}, fmt.Errorf("parse skill %q: %w", path, err)
	}

	var matter FrontMatter
	if err := yaml.Unmarshal(front, &matter); err != nil {
		return Skill{}, fmt.Errorf("parse skill %q front matter: %w: %w", path, ErrInvalidSkill, err)
	}
	if err := matter.Validate(); err != nil {
		return Skill{}, fmt.Errorf("parse skill %q: %w", path, err)
	}

	sum := sha256.Sum256(raw)
	return Skill{
		FrontMatter: matter,
		Path:        path,
		SHA256:      hex.EncodeToString(sum[:]),
		Body:        strings.TrimLeft(body, "\r\n"),
	}, nil
}

// splitFrontMatter returns the YAML block and the remaining body.
func splitFrontMatter(raw []byte) ([]byte, string, error) {
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	text := string(normalized)

	opening, ok := trimOpeningDelimiter(text)
	if !ok {
		return nil, "", fmt.Errorf("missing %q front matter delimiter: %w", frontMatterDelimiter, ErrInvalidSkill)
	}

	closing := strings.Index("\n"+opening, "\n"+frontMatterDelimiter)
	if closing < 0 {
		return nil, "", fmt.Errorf("unterminated front matter: %w", ErrInvalidSkill)
	}

	return []byte(opening[:closing]), opening[closing+len(frontMatterDelimiter):], nil
}

// trimOpeningDelimiter strips a leading delimiter line, tolerating a UTF-8 BOM
// and leading blank lines.
func trimOpeningDelimiter(text string) (string, bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.TrimLeft(text, "\n")

	if !strings.HasPrefix(text, frontMatterDelimiter) {
		return "", false
	}
	rest := text[len(frontMatterDelimiter):]
	if rest != "" && rest[0] != '\n' && rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}
	return strings.TrimLeft(rest, "\n"), true
}

// Validate enforces the required-field rules from CORE.md section 8.2.
func (m FrontMatter) Validate() error {
	required := []struct{ field, value string }{
		{"name", m.Name},
		{"category", string(m.Category)},
		{"description", m.Description},
		{"when_to_use", m.WhenToUse},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return fmt.Errorf("required field %q is blank: %w", r.field, ErrInvalidSkill)
		}
	}
	if !m.Category.Valid() {
		return fmt.Errorf("category %q: %w", m.Category, ErrInvalidSkill)
	}
	if len(m.StageAffinity) == 0 {
		return fmt.Errorf("required field %q is empty: %w", "stage_affinity", ErrInvalidSkill)
	}
	for _, stage := range m.StageAffinity {
		if !stage.Valid() {
			return fmt.Errorf("stage_affinity contains %q: %w", stage, ErrInvalidSkill)
		}
	}
	if m.TokenEstimate < 0 {
		return fmt.Errorf("token_estimate %d is negative: %w", m.TokenEstimate, ErrInvalidSkill)
	}
	return nil
}
