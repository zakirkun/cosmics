// Package skills ships the default playbook set in the binary so a fresh
// install has usable procedural memory before the operator writes any.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"sync"
)

//go:embed defaults/*.md
var defaultFS embed.FS

var (
	defaultsOnce sync.Once
	defaults     map[string][]byte
	defaultsErr  error
)

// DefaultFiles returns every embedded default skill keyed by its base filename.
//
// The map is a fresh copy, so a caller cannot mutate the shared set. An empty
// map means the binary was built without the defaults directory; DefaultSet
// reports that condition explicitly for callers that must not proceed silently.
func DefaultFiles() map[string][]byte {
	files, _ := DefaultSet()
	return files
}

// DefaultSet returns the embedded defaults and the read error, if any.
func DefaultSet() (map[string][]byte, error) {
	defaultsOnce.Do(func() {
		defaults, defaultsErr = readDefaults()
	})

	files := make(map[string][]byte, len(defaults))
	for name, raw := range defaults {
		files[name] = raw
	}
	return files, defaultsErr
}

// DefaultNames returns the embedded filenames in lexical order.
func DefaultNames() []string {
	files := DefaultFiles()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// readDefaults reads every embedded markdown file once.
func readDefaults() (map[string][]byte, error) {
	paths, err := fs.Glob(defaultFS, "defaults/*.md")
	if err != nil {
		return nil, fmt.Errorf("glob embedded defaults: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no embedded default skills found")
	}
	sort.Strings(paths)

	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		raw, err := defaultFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read embedded default %s: %w", path, err)
		}
		files[pathBase(path)] = raw
	}
	return files, nil
}

// pathBase returns the final slash-separated element of an embed path.
func pathBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
