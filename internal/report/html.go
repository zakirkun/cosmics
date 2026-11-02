package report

import (
	"fmt"
	"html"
	"strings"
)

// RenderHTML renders a report as a standalone HTML document.
//
// Every interpolated value passes through html.EscapeString. That is the whole
// defence against a finding title containing markup, which is why the document is
// assembled explicitly rather than through a template whose escaping could later be
// relaxed for convenience.
func RenderHTML(report Report) []byte {
	var builder strings.Builder

	builder.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	builder.WriteString("<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&builder, "<title>%s</title>\n", html.EscapeString(report.Name))
	builder.WriteString("<style>\n")
	builder.WriteString(htmlStyle)
	builder.WriteString("</style>\n</head>\n<body>\n")

	writeHTMLHeader(&builder, report)
	writeHTMLSummary(&builder, report)
	writeHTMLFindings(&builder, report)
	writeHTMLAssets(&builder, report)

	builder.WriteString("<footer>")
	builder.WriteString("Verified means a proof-of-concept artifact exists and the run that " +
		"produced it exited zero. An unverified finding is a lead, not a conclusion.")
	builder.WriteString("</footer>\n")

	builder.WriteString("</body>\n</html>\n")

	return []byte(builder.String())
}

// htmlStyle is inlined so the document is one self-contained file.
const htmlStyle = `body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;max-width:52rem;margin:2rem auto;padding:0 1rem;line-height:1.55;color:#1a1a1a}
h1{font-size:1.7rem;margin-bottom:.25rem}
h2{font-size:1.25rem;margin-top:2.5rem;border-bottom:1px solid #ddd;padding-bottom:.3rem}
h3{font-size:1.05rem;margin-top:2rem}
h4{font-size:.95rem;margin:1rem 0 .25rem}
table{border-collapse:collapse;margin:1rem 0}
th,td{border:1px solid #ddd;padding:.35rem .7rem;text-align:left}
th{background:#f4f4f4}
code{background:#f4f4f4;padding:.1rem .3rem;border-radius:3px;font-size:.92em}
dl{display:grid;grid-template-columns:11rem 1fr;gap:.2rem .8rem}
dt{font-weight:600}
dd{margin:0}
.critical{color:#8b0000;font-weight:600}
.high{color:#b34700;font-weight:600}
.medium{color:#7a5c00}
.low{color:#33517a}
.info{color:#555}
footer{margin-top:3rem;border-top:1px solid #ddd;padding-top:1rem;font-size:.9rem;color:#555}
`

// writeHTMLHeader writes the title and the engagement facts.
func writeHTMLHeader(builder *strings.Builder, report Report) {
	fmt.Fprintf(builder, "<h1>%s</h1>\n", html.EscapeString(report.Name))

	builder.WriteString("<dl>\n")
	writeHTMLFact(builder, "Client", report.Client)
	writeHTMLFact(builder, "Mode", report.Mode)
	writeHTMLFact(builder, "Authorized by", report.AuthorizedBy)
	writeHTMLFact(builder, "Authorized at", formatInstant(report.AuthorizedAt))
	writeHTMLFact(builder, "Generated at", formatInstant(report.GeneratedAt))
	builder.WriteString("</dl>\n")
}

// writeHTMLFact writes one term and definition.
func writeHTMLFact(builder *strings.Builder, term, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(builder, "<dt>%s</dt><dd>%s</dd>\n",
		html.EscapeString(term), html.EscapeString(value))
}

// writeHTMLSummary writes the executive summary and severity totals.
func writeHTMLSummary(builder *strings.Builder, report Report) {
	builder.WriteString("<h2>Executive summary</h2>\n")
	fmt.Fprintf(builder, "<p>%s</p>\n", html.EscapeString(report.ExecutiveSummary))

	builder.WriteString("<table>\n<thead><tr><th>Severity</th><th>Count</th></tr></thead>\n<tbody>\n")
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		if count := report.BySeverity[severity]; count > 0 {
			fmt.Fprintf(builder, "<tr><td class=\"%s\">%s</td><td>%d</td></tr>\n",
				severity, severity, count)
		}
	}
	fmt.Fprintf(builder, "<tr><td><strong>total</strong></td><td><strong>%d</strong></td></tr>\n",
		report.TotalFindings)
	builder.WriteString("</tbody>\n</table>\n")
}

// writeHTMLFindings writes one article per finding.
func writeHTMLFindings(builder *strings.Builder, report Report) {
	builder.WriteString("<h2>Findings</h2>\n")

	if len(report.Findings) == 0 {
		builder.WriteString("<p>No findings were recorded.</p>\n")
		return
	}

	for index, finding := range report.Findings {
		fmt.Fprintf(builder, "<article>\n<h3>%d. %s</h3>\n",
			index+1, html.EscapeString(finding.Title))

		builder.WriteString("<dl>\n")
		fmt.Fprintf(builder, "<dt>Severity</dt><dd class=\"%s\">%s</dd>\n",
			html.EscapeString(finding.Severity), html.EscapeString(finding.Severity))
		writeHTMLFact(builder, "Confidence", finding.Confidence)
		writeHTMLFact(builder, "Category", finding.Category)
		writeHTMLFact(builder, "Status", finding.Status)
		writeHTMLFact(builder, "Verified", yesNo(finding.Verified))

		if finding.CVSSVector != "" {
			fmt.Fprintf(builder, "<dt>CVSS</dt><dd><code>%s</code> %.1f</dd>\n",
				html.EscapeString(finding.CVSSVector), finding.CVSSScore)
		}
		if finding.Endpoint != "" {
			fmt.Fprintf(builder, "<dt>Endpoint</dt><dd><code>%s</code></dd>\n",
				html.EscapeString(finding.Endpoint))
		}
		if finding.Parameter != "" {
			fmt.Fprintf(builder, "<dt>Parameter</dt><dd><code>%s</code></dd>\n",
				html.EscapeString(finding.Parameter))
		}
		builder.WriteString("</dl>\n")

		writeHTMLProse(builder, "Description", finding.Description)
		writeHTMLProse(builder, "Impact", finding.Impact)
		writeHTMLProse(builder, "Reproduction", finding.Reproduction)
		writeHTMLProse(builder, "Remediation", finding.Remediation)

		builder.WriteString("</article>\n")
	}
}

// writeHTMLProse writes one optional paragraph section.
func writeHTMLProse(builder *strings.Builder, title, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(builder, "<h4>%s</h4>\n<p>%s</p>\n",
		html.EscapeString(title), html.EscapeString(body))
}

// writeHTMLAssets writes the asset inventory table.
func writeHTMLAssets(builder *strings.Builder, report Report) {
	if len(report.Assets) == 0 {
		return
	}

	builder.WriteString("<h2>Asset inventory</h2>\n")
	builder.WriteString("<table>\n<thead><tr><th>Kind</th><th>Value</th><th>State</th></tr></thead>\n<tbody>\n")

	for _, asset := range report.Assets {
		fmt.Fprintf(builder, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(asset.Kind),
			html.EscapeString(asset.Value),
			html.EscapeString(asset.State))
	}
	builder.WriteString("</tbody>\n</table>\n")
}
