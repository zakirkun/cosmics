package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// spread places left and right at the extremes of width.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return left + strings.Repeat(" ", gap) + right
}

// truncate clips plain text to width columns, marking the cut with an ellipsis.
// It measures with lipgloss.Width so wide runes never overflow a column.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// padCell truncates a value to width and right-pads it, so columns stay aligned.
func padCell(s string, width int) string {
	cell := truncate(s, width)
	if gap := width - lipgloss.Width(cell); gap > 0 {
		cell += strings.Repeat(" ", gap)
	}
	return cell
}

// limitLines keeps at most n lines so a pane never exceeds its budget.
func limitLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

func clampInt(v, low, high int) int {
	if high < low {
		low, high = high, low
	}
	return min(high, max(low, v))
}
