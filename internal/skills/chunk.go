package skills

import "strings"

// DefaultChunkTokens is the chunk ceiling from CORE.md section 8.3.
const DefaultChunkTokens = 800

// Chunks splits the body on H2 headings into at most maxTokens estimated words
// per chunk.
//
// Token count is approximated as word count, which is close enough for budget
// accounting and needs no tokenizer. Small sections are joined with the next one
// up to the ceiling; an oversized section is split on paragraph, then sentence,
// then word boundaries, so no chunk exceeds the bound.
func (s Skill) Chunks(maxTokens int) []string {
	if maxTokens <= 0 {
		maxTokens = DefaultChunkTokens
	}

	chunks := make([]string, 0, 4)
	current := make([]string, 0, 4)
	used := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, strings.TrimSpace(strings.Join(current, "\n\n")))
		current = nil
		used = 0
	}

	for _, section := range splitSections(s.Body) {
		tokens := countWords(section)
		if tokens > maxTokens {
			flush()
			chunks = append(chunks, splitOversized(section, maxTokens)...)
			continue
		}
		if used+tokens > maxTokens {
			flush()
		}
		current = append(current, section)
		used += tokens
	}
	flush()

	// A body with no headings still yields its preamble as one chunk.
	return chunks
}

// splitSections groups the body into heading-led sections, keeping any preamble
// as its own section.
func splitSections(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	sections := make([]string, 0, 8)
	current := make([]string, 0, 8)

	flush := func() {
		text := strings.TrimSpace(strings.Join(current, "\n"))
		if text != "" {
			sections = append(sections, text)
		}
		current = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
		}
		current = append(current, line)
	}
	flush()
	return sections
}

// splitOversized cuts one over-sized section into pieces under maxTokens.
//
// The heading is rejoined to the first piece, so its words are reserved out of
// the body budget; otherwise prepending it would push that piece back over the
// ceiling it was just split to satisfy.
func splitOversized(section string, maxTokens int) []string {
	if countWords(section) <= maxTokens {
		return []string{section}
	}

	heading, rest := "", section
	if newline := strings.IndexByte(section, '\n'); strings.HasPrefix(section, "## ") && newline >= 0 {
		heading, rest = strings.TrimSpace(section[:newline]), section[newline+1:]
	}
	bodyCeiling := max(1, maxTokens-countWords(heading))

	var (
		pieces  []string
		current []string
		used    int
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		pieces = append(pieces, strings.Join(current, " "))
		current = nil
		used = 0
	}

	for _, unit := range splitUnits(rest, bodyCeiling) {
		tokens := countWords(unit)
		switch {
		case tokens > bodyCeiling:
			flush()
			pieces = append(pieces, hardSplit(unit, bodyCeiling)...)
		case used+tokens > bodyCeiling:
			flush()
			current = append(current, unit)
			used = tokens
		default:
			current = append(current, unit)
			used += tokens
		}
	}
	flush()

	if heading != "" && len(pieces) > 0 {
		pieces[0] = heading + "\n\n" + pieces[0]
	}
	return pieces
}

// splitUnits breaks text into paragraph, then sentence units.
func splitUnits(text string, ceiling int) []string {
	paragraphs := strings.Split(text, "\n\n")
	units := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if countWords(paragraph) <= ceiling {
			units = append(units, paragraph)
			continue
		}
		units = append(units, splitSentences(paragraph)...)
	}
	return units
}

// splitSentences breaks a paragraph on sentence terminators.
func splitSentences(paragraph string) []string {
	out := make([]string, 0, 8)
	var b strings.Builder
	for _, r := range paragraph {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		}
	}
	if tail := strings.TrimSpace(b.String()); tail != "" {
		out = append(out, tail)
	}
	return out
}

// hardSplit cuts text by words when it has no usable internal boundary.
func hardSplit(text string, maxTokens int) []string {
	words := strings.Fields(text)
	pieces := make([]string, 0, len(words)/maxTokens+1)
	for start := 0; start < len(words); start += maxTokens {
		end := min(start+maxTokens, len(words))
		pieces = append(pieces, strings.Join(words[start:end], " "))
	}
	return pieces
}

// countWords approximates a token count by counting whitespace-separated words.
func countWords(text string) int {
	return len(strings.Fields(text))
}
