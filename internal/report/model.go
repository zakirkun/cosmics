package report

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinel errors for report building.
var (
	ErrInvalidReport = errors.New("report: invalid report")
)

// Finding is one item of the report.
//
// It deliberately carries no credential value and no raw tool output: a report is
// a document an operator may hand to a client, so it holds conclusions and
// references, not secrets or megabytes of tool noise.
type Finding struct {
	ID           domain.ID
	Title        string
	Category     string
	Severity     string
	Confidence   string
	Status       string
	CVSSVector   string
	CVSSScore    float64
	Description  string
	Impact       string
	Remediation  string
	Reproduction string
	Endpoint     string
	Parameter    string

	Verified bool
}

// Asset is one inventory line.
type Asset struct {
	Kind  string
	Value string
	State string
}

// Report is the rendered document's model.
//
// GeneratedAt is a value, not a call: rendering must be reproducible, so the
// instant is decided once by the caller and never re-read while building output.
type Report struct {
	EngagementID domain.ID
	Name         string
	Client       string
	Mode         string
	AuthorizedBy string
	AuthorizedAt domain.TimestampMs

	GeneratedAt domain.TimestampMs

	ExecutiveSummary string
	Findings         []Finding
	Assets           []Asset
	Facts            []string

	VerifiedCount int
	TotalFindings int
	BySeverity    map[string]int
}

// Deps are the readers a report build needs.
type Deps struct {
	Engagements interface {
		Get(ctx context.Context, id domain.ID) (domain.Engagement, error)
	}
	Findings interface {
		List(ctx context.Context, engagementID domain.ID) ([]store.Finding, error)
	}
	Assets interface {
		List(ctx context.Context, engagementID domain.ID) ([]store.Asset, error)
	}
	Clock domain.Clock
}

// severityRank orders severities from most to least urgent.
//
// A lexical sort would place "critical" before "high" by luck and "info" before
// "low" by luck; this ranking is the ordering the document must show.
var severityRank = map[string]int{
	"critical": 0,
	"high":     1,
	"medium":   2,
	"low":      3,
	"info":     4,
}

// Build reads an engagement and assembles its report.
func Build(ctx context.Context, deps Deps, engagementID domain.ID) (Report, error) {
	if deps.Engagements == nil {
		return Report{}, fmt.Errorf("engagements reader: %w", ErrInvalidReport)
	}
	if deps.Clock == nil {
		return Report{}, fmt.Errorf("clock: %w", ErrInvalidReport)
	}

	engagement, err := deps.Engagements.Get(ctx, engagementID)
	if err != nil {
		return Report{}, fmt.Errorf("read engagement %s: %w", engagementID, err)
	}

	report := Report{
		EngagementID: engagement.ID,
		Name:         engagement.Name,
		Client:       engagement.Client,
		Mode:         string(engagement.Mode),
		AuthorizedBy: engagement.Authorization.AuthorizedBy,
		AuthorizedAt: engagement.Authorization.AuthorizedAt,
		GeneratedAt:  deps.Clock.Now(),
		BySeverity:   make(map[string]int, len(severityRank)),
	}

	if deps.Findings != nil {
		findings, err := deps.Findings.List(ctx, engagementID)
		if err != nil {
			return Report{}, fmt.Errorf("list findings: %w", err)
		}
		report.addFindings(findings)
	}

	if deps.Assets != nil {
		assets, err := deps.Assets.List(ctx, engagementID)
		if err != nil {
			return Report{}, fmt.Errorf("list assets: %w", err)
		}
		report.Assets = toAssets(assets)
	}

	report.sortFindings()
	report.summarize()

	return report, nil
}

func (r *Report) addFindings(rows []store.Finding) {
	for _, row := range rows {
		score := 0.0
		if row.CVSSScore != nil {
			score = *row.CVSSScore
		}

		r.Findings = append(r.Findings, Finding{
			ID:           row.ID,
			Title:        row.Title,
			Category:     row.Category,
			Severity:     row.Severity,
			Confidence:   row.Confidence,
			Status:       row.Status,
			CVSSVector:   row.CVSSVector,
			CVSSScore:    score,
			Description:  row.Description,
			Impact:       row.Impact,
			Remediation:  row.Remediation,
			Reproduction: row.Reproduction,
			Endpoint:     row.Endpoint,
			Parameter:    row.Parameter,
			Verified:     row.Verified,
		})
	}
}

// sortFindings orders by severity then title then identifier.
//
// The identifier is the final tiebreak so two findings with the same severity and
// title cannot swap places between runs.
func (r *Report) sortFindings() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		left, right := r.Findings[i], r.Findings[j]
		if severityRank[left.Severity] != severityRank[right.Severity] {
			return severityRank[left.Severity] < severityRank[right.Severity]
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		return left.ID.String() < right.ID.String()
	})
}

// summarize counts findings and writes the executive summary.
func (r *Report) summarize() {
	r.TotalFindings = len(r.Findings)

	for _, finding := range r.Findings {
		r.BySeverity[finding.Severity]++
		if finding.Verified {
			r.VerifiedCount++
		}
	}

	r.ExecutiveSummary = summaryText(*r)
}

// summaryText writes a factual sentence about the engagement.
//
// Every number comes from the rows. A summary that overstates what was proven is
// the failure mode this document must not have, so no model involvement is allowed
// anywhere in this function.
func summaryText(r Report) string {
	if r.TotalFindings == 0 {
		return fmt.Sprintf(
			"No findings were recorded for %s. An empty report means no finding was "+
				"produced; it does not mean the targets are free of vulnerabilities.",
			r.Name)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s produced %d finding", r.Name, r.TotalFindings)
	if r.TotalFindings != 1 {
		builder.WriteString("s")
	}
	fmt.Fprintf(&builder, ", of which %d %s verified by proof-of-concept evidence",
		r.VerifiedCount, plural(r.VerifiedCount, "is", "are"))

	if counts := r.severityBreakdown(); counts != "" {
		builder.WriteString(" (")
		builder.WriteString(counts)
		builder.WriteString(")")
	}
	builder.WriteString(".")
	return builder.String()
}

// severityBreakdown renders the non-zero severity counts in rank order.
func (r Report) severityBreakdown() string {
	order := []string{"critical", "high", "medium", "low", "info"}

	parts := make([]string, 0, len(order))
	for _, severity := range order {
		if count := r.BySeverity[severity]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, severity))
		}
	}
	return strings.Join(parts, ", ")
}

// plural picks the singular or plural word.
func plural(count int, singular, pluralForm string) string {
	if count == 1 {
		return singular
	}
	return pluralForm
}

// toAssets converts stored assets, sorted by kind then value.
func toAssets(rows []store.Asset) []Asset {
	out := make([]Asset, 0, len(rows))
	for _, row := range rows {
		out = append(out, Asset{Kind: row.Kind, Value: row.Value, State: row.State})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Value < out[j].Value
	})
	return out
}
