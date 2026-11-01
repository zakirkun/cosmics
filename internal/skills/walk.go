package skills

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
)

// markdownFiles returns every .md path under root in lexical order.
func markdownFiles(root string) ([]string, error) {
	paths := make([]string, 0, 32)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// escapesRoot reports whether path resolves outside root through a symlink.
func escapesRoot(root, path string) (bool, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, fmt.Errorf("resolve root: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return true, nil
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

func newTimestamp() domain.TimestampMs {
	return domain.TimestampMs(time.Now().UTC().UnixMilli())
}
