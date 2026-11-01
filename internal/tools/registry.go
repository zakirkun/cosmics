package tools

import (
	"embed"
	"fmt"
	"sort"
	"sync"

	"github.com/cosmics/cosmics/internal/domain"
)

//go:embed registry/*.yaml
var registryFS embed.FS

// Registry is an immutable catalog of tools keyed by identifier.
type Registry struct {
	byID   map[string]Tool
	sorted []Tool
}

var (
	loadOnce   sync.Once
	defaultReg *Registry
	loadErr    error
)

// Default returns the embedded registry, loading it on first use.
//
// Loading is memoised because the registry is immutable: re-parsing the same
// embedded bytes on every call would be pure waste.
func Default() (*Registry, error) {
	loadOnce.Do(func() {
		defaultReg, loadErr = LoadRegistry(registryFS)
	})
	if loadErr != nil {
		return nil, loadErr
	}
	return defaultReg, nil
}

// LoadRegistry parses and validates every embedded tool definition.
func LoadRegistry(fsys embed.FS) (*Registry, error) {
	entries, err := fsys.ReadDir("registry")
	if err != nil {
		return nil, fmt.Errorf("read registry directory: %w", err)
	}

	byID := make(map[string]Tool, len(entries))
	sorted := make([]Tool, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		raw, err := fsys.ReadFile("registry/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		tool, err := parseTool(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		if err := tool.Validate(); err != nil {
			return nil, fmt.Errorf("validate %s: %w", entry.Name(), err)
		}
		if _, exists := byID[tool.ID]; exists {
			return nil, fmt.Errorf("duplicate tool id %q in %s: %w", tool.ID, entry.Name(), ErrInvalidTool)
		}

		byID[tool.ID] = tool
		sorted = append(sorted, tool)
	}

	if len(byID) == 0 {
		return nil, fmt.Errorf("registry is empty: %w", ErrInvalidTool)
	}

	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	return &Registry{byID: byID, sorted: sorted}, nil
}

// Get returns the tool with the given identifier.
func (r *Registry) Get(id string) (Tool, error) {
	tool, ok := r.byID[id]
	if !ok {
		return Tool{}, fmt.Errorf("tool %q: %w", id, ErrToolNotFound)
	}
	return tool, nil
}

// All returns every tool, sorted by identifier for deterministic output.
func (r *Registry) All() []Tool {
	out := make([]Tool, len(r.sorted))
	copy(out, r.sorted)
	return out
}

// Len reports how many tools the registry holds.
func (r *Registry) Len() int { return len(r.sorted) }

// ForCategory returns every enabled tool in the given category.
func (r *Registry) ForCategory(category Category) []Tool {
	out := make([]Tool, 0, len(r.sorted))
	for _, tool := range r.sorted {
		if tool.Category == category && tool.Enabled {
			out = append(out, tool)
		}
	}
	return out
}

// stageCategories is the stage allow-list from CORE.md section 5.2.
//
// Exploit and post tools are listed separately because they are gated by the
// engagement mode as well as by the stage.
var stageCategories = map[domain.StageKind][]Category{
	domain.StageRecon:     {CategoryRecon, CategoryNetwork, CategoryWeb},
	domain.StageOSINT:     {CategoryOSINT},
	domain.StageAnalysis:  nil,
	domain.StageMapper:    nil,
	domain.StageExploit:   {CategoryExploit, CategoryWeb},
	domain.StageReporting: nil,
}

// allowedStageCategories returns the categories a stage may use.
func allowedStageCategories(stage domain.StageKind) []Category {
	return stageCategories[stage]
}

// ForStageAllowList returns the tools a stage may use under the given mode.
//
// The mode gate is applied here rather than at execution time so the model never
// sees a tool it is not allowed to call: offering an unusable tool wastes a turn
// and produces a confusing error instead of a plan.
func (r *Registry) ForStageAllowList(stage domain.StageKind, mode domain.Mode) []Tool {
	if !mode.IncludesStage(stage) {
		return nil
	}

	allowed := allowedStageCategories(stage)
	if len(allowed) == 0 {
		return nil
	}

	allowSet := make(map[Category]struct{}, len(allowed))
	for _, category := range allowed {
		allowSet[category] = struct{}{}
	}

	out := make([]Tool, 0, len(r.sorted))
	for _, tool := range r.sorted {
		if !tool.Enabled {
			continue
		}
		if _, ok := allowSet[tool.Category]; !ok {
			continue
		}
		if tool.Category == CategoryPost && !mode.AllowsPostExploitation() {
			continue
		}
		if tool.Category == CategoryExploit && !mode.AllowsExploitation() {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// Authorize reports whether a tool may run in a stage under a mode.
func (r *Registry) Authorize(toolID string, stage domain.StageKind, mode domain.Mode) error {
	tool, err := r.Get(toolID)
	if err != nil {
		return err
	}
	if !tool.Enabled {
		return fmt.Errorf("tool %q: %w", toolID, ErrToolDisabled)
	}

	if tool.Category == CategoryPost && !mode.AllowsPostExploitation() {
		return fmt.Errorf("tool %q needs full mode: %w", toolID, ErrStageForbidden)
	}
	if tool.Category == CategoryExploit && !mode.AllowsExploitation() {
		return fmt.Errorf("tool %q needs exploit mode: %w", toolID, ErrStageForbidden)
	}

	allowed := allowedStageCategories(stage)
	for _, category := range allowed {
		if category == tool.Category {
			return nil
		}
	}
	return fmt.Errorf("tool %q is %s, stage %q allows %v: %w",
		toolID, tool.Category, stage, allowed, ErrStageForbidden)
}
