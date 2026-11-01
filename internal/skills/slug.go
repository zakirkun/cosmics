package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Slug derives the stable skills.id from the front matter name.
func (s Skill) Slug() string {
	var b strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(s.Name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousDash = false
		case !previousDash && b.Len() > 0:
			b.WriteByte('-')
			previousDash = true
		}
	}
	slug := strings.TrimSuffix(b.String(), "-")
	if slug == "" {
		return strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path))
	}
	return slug
}

// ReadFile parses one skill file from disk.
func ReadFile(path string) (Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("read skill %q: %w", path, err)
	}
	return ParseSkill(path, raw)
}
