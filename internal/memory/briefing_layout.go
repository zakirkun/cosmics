package memory

import (
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// assembleBriefing joins sections under limit bytes, cutting with a marker.
func assembleBriefing(sections []briefingSection, limit int) string {
	headers := make([]string, len(sections))
	for i, section := range sections {
		headers[i] = "## " + section.title + "\n"
	}

	// afterCut[i] is the byte cost of cutting section i short: its marker line
	// plus, for every later section, that section's header and marker.
	afterCut := make([]int, len(sections))
	for i := range sections {
		cost := len(truncatedMarker) + 1
		for j := i + 1; j < len(sections); j++ {
			cost += len(headers[j]) + len(truncatedMarker) + 1
		}
		afterCut[i] = cost
	}

	var b strings.Builder
	for i, section := range sections {
		if b.Len()+len(headers[i])+afterCut[i] > limit {
			writeCut(&b, i, false, headers)
			return b.String()
		}
		b.WriteString(headers[i])

		for _, line := range section.lines {
			if b.Len()+len(line)+1+afterCut[i] > limit {
				writeCut(&b, i, true, headers)
				return b.String()
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// writeCut emits the truncation marker and announces every later section.
func writeCut(b *strings.Builder, from int, headerWritten bool, headers []string) {
	if !headerWritten {
		b.WriteString(headers[from])
	}
	b.WriteString(truncatedMarker)
	b.WriteByte('\n')
	for j := from + 1; j < len(headers); j++ {
		b.WriteString(headers[j])
		b.WriteString(truncatedMarker)
		b.WriteByte('\n')
	}
}

// relativeTime renders the age of an instant relative to now.
func relativeTime(now, then domain.TimestampMs) string {
	if then.IsZero() {
		return "never"
	}
	delta := int64(now) - int64(then)
	switch {
	case delta <= 0:
		return "just now"
	case delta < 60_000:
		return fmt.Sprintf("%ds ago", delta/1000)
	case delta < 3_600_000:
		return fmt.Sprintf("%dm ago", delta/60_000)
	case delta < 86_400_000:
		return fmt.Sprintf("%dh ago", delta/3_600_000)
	default:
		return fmt.Sprintf("%dd ago", delta/86_400_000)
	}
}

// formatElapsed renders a positive duration in the largest two units.
func formatElapsed(delta domain.TimestampMs) string {
	seconds := int64(delta) / 1000
	switch {
	case seconds <= 0:
		return "0s"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", seconds/3600, (seconds%3600)/60)
	}
}

// formatCount renders an integer with no separators; the briefing is parsed by a
// model, which does not benefit from locale grouping.
func formatCount(n int64) string { return fmt.Sprintf("%d", n) }

// formatUSD renders micro-USD as dollars.
func formatUSD(micros int64) string { return fmt.Sprintf("$%.2f", float64(micros)/1_000_000) }

// clip shortens s to limit runes with the truncation marker.
func clip(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + " " + truncatedMarker
}
