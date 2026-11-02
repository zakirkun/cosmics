package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cosmics/cosmics/internal/domain"
)

const (
	colFactConfidenceWidth = 10
	colFactUpdatedWidth    = 16
	minMemoryWidth         = 40
)

// FactView is one memory row.
type FactView struct {
	Key        string
	Value      string
	Confidence string
	UpdatedAt  domain.TimestampMs
}

// MemoryView is the memory screen state: rows and a cursor.
type MemoryView struct {
	facts  []FactView
	cursor int
}

// NewMemoryView returns an empty view.
func NewMemoryView() MemoryView { return MemoryView{} }

// WithFacts replaces the rows, ordered by key then value.
func (v MemoryView) WithFacts(facts []FactView) MemoryView {
	sorted := make([]FactView, len(facts))
	copy(sorted, facts)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].Value < sorted[j].Value
	})

	v.facts = sorted
	v.cursor = clampInt(v.cursor, 0, max(0, len(sorted)-1))
	return v
}

// MoveCursor moves the cursor by delta, clamped to the rows.
func (v MemoryView) MoveCursor(delta int) MemoryView {
	v.cursor = clampInt(v.cursor+delta, 0, max(0, len(v.facts)-1))
	return v
}

// Facts returns the rows in key order.
func (v MemoryView) Facts() []FactView { return v.facts }

// Cursor returns the index of the highlighted row.
func (v MemoryView) Cursor() int { return v.cursor }

// Selected returns the row under the cursor.
func (v MemoryView) Selected() (FactView, bool) {
	if v.cursor < 0 || v.cursor >= len(v.facts) {
		return FactView{}, false
	}
	return v.facts[v.cursor], true
}

// RenderMemory renders facts grouped by key prefix, in group then key order.
func RenderMemory(v MemoryView, width int) string {
	width = max(1, width)

	if len(v.facts) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			styleTitle.Render("No facts recorded"),
			"",
			styleMuted.Width(width).Render(
				"Memory fills as stages observe the target. Run an engagement — "+
					"cosmics run --engagement <id> — then reload this screen with r."),
		)
	}

	groups := groupFacts(v.facts)

	lines := make([]string, 0, len(v.facts)+len(groups))
	for _, group := range groups {
		lines = append(lines, styleBrand.Render(group.prefix))
		for _, index := range group.indexes {
			lines = append(lines, renderFactRow(v.facts[index], index == v.cursor, width))
		}
	}
	return strings.Join(lines, "\n")
}

// factGroup is one key prefix with the flat indexes of its rows.
type factGroup struct {
	prefix  string
	indexes []int
}

// groupFacts buckets rows by key prefix and orders the groups by prefix, so
// rendering never depends on map iteration order.
func groupFacts(facts []FactView) []factGroup {
	indexes := make(map[string][]int, len(facts))
	for i, fact := range facts {
		prefix := factKeyPrefix(fact.Key)
		indexes[prefix] = append(indexes[prefix], i)
	}

	prefixes := make([]string, 0, len(indexes))
	for prefix := range indexes {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)

	groups := make([]factGroup, 0, len(prefixes))
	for _, prefix := range prefixes {
		groups = append(groups, factGroup{prefix: prefix, indexes: indexes[prefix]})
	}
	return groups
}

// factKeyPrefix is the grouping key: the text before the first separator.
func factKeyPrefix(key string) string {
	if prefix, _, found := strings.Cut(key, "."); found && prefix != "" {
		return prefix
	}
	if strings.TrimSpace(key) == "" {
		return "(unkeyed)"
	}
	return key
}

// renderFactRow renders one fact as aligned key and value columns.
func renderFactRow(fact FactView, selected bool, width int) string {
	if width < minMemoryWidth {
		line := truncate(fact.Key+" = "+fact.Value, width)
		if selected {
			return styleSelected.Render(padCell(line, width))
		}
		return line
	}

	keyWidth := max(tableMinNameCol, width/4)
	valueWidth := max(1, width-keyWidth-colFactConfidenceWidth-colFactUpdatedWidth-tableGutter*3)

	cells := []string{
		padCell(fact.Key, keyWidth),
		padCell(fact.Value, valueWidth),
		padCell(fact.Confidence, colFactConfidenceWidth),
		padCell(memoryInstant(fact.UpdatedAt), colFactUpdatedWidth),
	}
	line := strings.Join(cells, strings.Repeat(" ", tableGutter))

	if selected {
		return styleSelected.Render(padCell(line, width))
	}
	return line
}

// memoryInstant renders an update time as a UTC wall clock, or a dash.
func memoryInstant(at domain.TimestampMs) string {
	if at.IsZero() {
		return emDash
	}
	return at.Time().Format("2006-01-02 15:04")
}
