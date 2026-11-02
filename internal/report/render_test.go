package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/report"
)

// reportFixture builds a fully populated report with a fixed instant.
func reportFixture() report.Report {
	engagementID := domain.NewULIDGenerator().New()

	return report.Report{
		EngagementID:     engagementID,
		Name:             "Acme external assessment",
		Client:           "Acme Corporation",
		Mode:             "full",
		AuthorizedBy:     "dana.reyes",
		AuthorizedAt:     1_759_999_000_000,
		GeneratedAt:      1_760_000_000_000,
		ExecutiveSummary: "Acme external assessment produced 2 findings.",
		TotalFindings:    2,
		VerifiedCount:    1,
		BySeverity:       map[string]int{"high": 1, "medium": 1},
		Findings: []report.Finding{
			{
				ID:           domain.NewULIDGenerator().New(),
				Title:        "SQL injection in the login form",
				Category:     "web",
				Severity:     "high",
				Confidence:   "confirmed",
				Status:       "open",
				CVSSVector:   "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
				CVSSScore:    9.8,
				Description:  "The username parameter is concatenated into the query.",
				Impact:       "An attacker can read and modify the database.",
				Reproduction: "POST /login with username=admin' OR '1'='1",
				Remediation:  "Use a parameterised query.",
				Endpoint:     "https://portal.acme.test/login",
				Parameter:    "username",
				Verified:     true,
			},
			{
				ID:          domain.NewULIDGenerator().New(),
				Title:       "Missing security headers",
				Category:    "config",
				Severity:    "medium",
				Confidence:  "likely",
				Status:      "open",
				Description: "Strict-Transport-Security is absent.",
				Verified:    false,
			},
		},
		Assets: []report.Asset{
			{Kind: "host", Value: "portal.acme.test", State: "discovered"},
			{Kind: "url", Value: "https://portal.acme.test/login", State: "discovered"},
		},
	}
}

func Test_RenderMarkdown_is_byte_identical_when_called_twice_on_the_same_report(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	first := report.RenderMarkdown(reportValue)
	second := report.RenderMarkdown(reportValue)

	// Then
	assert.True(t, bytes.Equal(first, second),
		"a regenerated report must be byte-identical, otherwise it cannot be verified")
	assert.NotEmpty(t, first)
}

func Test_RenderHTML_is_byte_identical_when_called_twice_on_the_same_report(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	first := report.RenderHTML(reportValue)
	second := report.RenderHTML(reportValue)

	// Then
	assert.True(t, bytes.Equal(first, second))
	assert.NotEmpty(t, first)
}

func Test_RenderHTML_escapes_script_tags_when_a_finding_title_contains_one(t *testing.T) {
	// Given
	reportValue := reportFixture()
	reportValue.Findings[0].Title = `<script>alert("xss")</script>`

	// When
	rendered := string(report.RenderHTML(reportValue))

	// Then
	assert.NotContains(t, rendered, "<script>alert",
		"a finding title must never introduce executable markup")
	assert.Contains(t, rendered, "&lt;script&gt;")
}

func Test_RenderMarkdown_escapes_a_pipe_when_a_title_contains_one(t *testing.T) {
	// Given
	reportValue := reportFixture()
	reportValue.Findings[0].Title = "injection | in a table"

	// When
	rendered := string(report.RenderMarkdown(reportValue))

	// Then
	assert.Contains(t, rendered, `\|`)
	assert.NotContains(t, rendered, "injection | in a table",
		"an unescaped pipe would break the surrounding table")
}

func Test_RenderMarkdown_renders_every_finding_when_several_exist(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	rendered := string(report.RenderMarkdown(reportValue))

	// Then
	for _, finding := range reportValue.Findings {
		assert.Contains(t, rendered, finding.Title)
	}
	assert.Contains(t, rendered, "## Findings")
	assert.Contains(t, rendered, "## Asset inventory")
}

func Test_RenderMarkdown_states_that_an_empty_report_is_not_a_clean_bill_when_no_findings(t *testing.T) {
	// Given
	reportValue := report.Report{
		Name:             "empty engagement",
		Mode:             "recon",
		ExecutiveSummary: "No findings were recorded for empty engagement.",
		BySeverity:       map[string]int{},
	}

	// When
	rendered := string(report.RenderMarkdown(reportValue))

	// Then
	assert.Contains(t, rendered, "No findings were recorded")
}

func Test_RenderHTML_includes_every_asset_when_inventory_exists(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	rendered := string(report.RenderHTML(reportValue))

	// Then
	for _, asset := range reportValue.Assets {
		assert.Contains(t, rendered, asset.Value)
	}
	assert.Contains(t, rendered, "Asset inventory")
}

func Test_RenderHTML_is_a_complete_document_when_rendered(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	rendered := string(report.RenderHTML(reportValue))

	// Then
	assert.True(t, strings.HasPrefix(rendered, "<!DOCTYPE html>"))
	assert.True(t, strings.HasSuffix(rendered, "</html>\n"))
	assert.Contains(t, rendered, "<meta charset=\"utf-8\">")
}

func Test_RenderMarkdown_marks_a_verified_finding_when_true(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	rendered := string(report.RenderMarkdown(reportValue))

	// Then
	assert.Contains(t, rendered, "- **Verified:** yes")
	assert.Contains(t, rendered, "- **Verified:** no")
}

func Test_RenderMarkdown_includes_the_cvss_vector_when_present(t *testing.T) {
	// Given
	reportValue := reportFixture()

	// When
	rendered := string(report.RenderMarkdown(reportValue))

	// Then
	assert.Contains(t, rendered, "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H")
	assert.Contains(t, rendered, "9.8")
}
