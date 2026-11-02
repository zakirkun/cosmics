package report

import (
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// RenderMarkdown renders a report as Markdown.
//
// The function is a pure function of the Report value: no clock, no map ranging, no
// filesystem. Two calls with the same value therefore produce identical bytes,
// which is what makes a regenerated report verifiable.
func RenderMarkdown(report Report) []byte {
	var builder strings.Builder

	writeMarkdownHeader(&builder, report)
	writeMarkdownSummary(&builder, report)
	writeMarkdownFindings(&builder, report)
	writeMarkdownAssets(&builder, report)
	writeMarkdownAppendix(&builder)

	return []byte(builder.String())
}

// writeMarkdownHeader writes the title block.
func writeMarkdownHeader(builder *strings.Builder, report Report) {
	fmt.Fprintf(builder, "# %s\n\n", escapeMarkdown(report.Name))

	if report.Client != "" {
		fmt.Fprintf(builder, "**Client:** %s  \n", escapeMarkdown(report.Client))
	}
	fmt.Fprintf(builder, "**Mode:** %s  \n", escapeMarkdown(report.Mode))
	fmt.Fprintf(builder, "**Authorized by:** %s  \n", escapeMarkdown(report.AuthorizedBy))
	fmt.Fprintf(builder, "**Authorized at:** %s  \n", formatInstant(report.AuthorizedAt))
	fmt.Fprintf(builder, "**Generated at:** %s\n\n", formatInstant(report.GeneratedAt))

	builder.WriteString("---\n\n")
}

// writeMarkdownSummary writes the executive summary and the totals.
func writeMarkdownSummary(builder *strings.Builder, report Report) {
	builder.WriteString("## Executive summary\n\n")
	builder.WriteString(escapeMarkdown(report.ExecutiveSummary))
	builder.WriteString("\n\n")

	builder.WriteString("| Severity | Count |\n|---|---|\n")
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		if count := report.BySeverity[severity]; count > 0 {
			fmt.Fprintf(builder, "| %s | %d |\n", severity, count)
		}
	}
	fmt.Fprintf(builder, "| **total** | **%d** |\n\n", report.TotalFindings)
}

// writeMarkdownFindings writes one section per finding.
func writeMarkdownFindings(builder *strings.Builder, report Report) {
	builder.WriteString("## Findings\n\n")

	if len(report.Findings) == 0 {
		builder.WriteString("No findings were recorded.\n\n")
		return
	}

	for index, finding := range report.Findings {
		fmt.Fprintf(builder, "### %d. %s\n\n", index+1, escapeMarkdown(finding.Title))

		fmt.Fprintf(builder, "- **Severity:** %s\n", finding.Severity)
		fmt.Fprintf(builder, "- **Confidence:** %s\n", finding.Confidence)
		fmt.Fprintf(builder, "- **Category:** %s\n", finding.Category)
		fmt.Fprintf(builder, "- **Status:** %s\n", finding.Status)
		fmt.Fprintf(builder, "- **Verified:** %s\n", yesNo(finding.Verified))

		if finding.CVSSVector != "" {
			fmt.Fprintf(builder, "- **CVSS:** %s (%.1f)\n", finding.CVSSVector, finding.CVSSScore)
		}
		if finding.Endpoint != "" {
			fmt.Fprintf(builder, "- **Endpoint:** `%s`\n", finding.Endpoint)
		}
		if finding.Parameter != "" {
			fmt.Fprintf(builder, "- **Parameter:** `%s`\n", finding.Parameter)
		}
		builder.WriteString("\n")

		writeMarkdownParagraph(builder, "Description", finding.Description)
		writeMarkdownParagraph(builder, "Impact", finding.Impact)
		writeMarkdownParagraph(builder, "Reproduction", finding.Reproduction)
		writeMarkdownParagraph(builder, "Remediation", finding.Remediation)
	}
}

// writeMarkdownParagraph writes one optional prose section.
func writeMarkdownParagraph(builder *strings.Builder, title, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(builder, "**%s**\n\n%s\n\n", title, escapeMarkdown(body))
}

// writeMarkdownAssets writes the asset inventory.
func writeMarkdownAssets(builder *strings.Builder, report Report) {
	if len(report.Assets) == 0 {
		return
	}

	builder.WriteString("## Asset inventory\n\n")
	builder.WriteString("| Kind | Value | State |\n|---|---|---|\n")

	for _, asset := range report.Assets {
		fmt.Fprintf(builder, "| %s | %s | %s |\n",
			escapeMarkdown(asset.Kind),
			escapeMarkdown(asset.Value),
			escapeMarkdown(asset.State))
	}
	builder.WriteString("\n")
}

// writeMarkdownAppendix writes the closing notes.
func writeMarkdownAppendix(builder *strings.Builder) {
	builder.WriteString("---\n\n")
	builder.WriteString("Verified means a proof-of-concept artifact exists and the run that " +
		"produced it exited zero. An unverified finding is a lead, not a conclusion.\n")
}

// formatInstant renders a timestamp in UTC, or a dash when unset.
func formatInstant(at domain.TimestampMs) string {
	if at.IsZero() {
		return "—"
	}
	return at.Time().Format("2006-01-02 15:04:05 UTC")
}

// yesNo renders a bool as a word rather than a literal.
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// escapeMarkdown neutralises the characters that would otherwise change block
// structure, so a finding title containing a pipe cannot break the table above it
// and one containing a backtick cannot open a code span.
func escapeMarkdown(text string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"`", "\\`",
		"|", "\\|",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(text)
}
