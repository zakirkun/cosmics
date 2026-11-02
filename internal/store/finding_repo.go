package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// ErrInvalidFinding is returned when a finding row violates a column contract.
var ErrInvalidFinding = errors.New("store: invalid finding")

var findingCategories = []string{
	"web", "network", "cloud", "mobile", "crypto",
	"auth", "config", "data_exposure", "access_control",
}

// findingSeverities is ordered most severe first.
var findingSeverities = []string{"critical", "high", "medium", "low", "info"}

var findingConfidences = []string{"confirmed", "likely", "possible"}

var findingStatuses = []string{"open", "false_positive", "accepted_risk", "fixed", "retest"}

// IsFindingCategory reports whether category satisfies the findings CHECK.
func IsFindingCategory(category string) bool {
	return slices.Contains(findingCategories, category)
}

// Finding is one row of the findings table.
type Finding struct {
	ID           domain.ID
	EngagementID domain.ID
	StageRunID   domain.ID
	Title        string
	Category     string
	Severity     string
	Confidence   string
	CVSSVector   string
	CVSSScore    *float64
	AssetID      domain.ID
	Endpoint     string
	Parameter    string
	Description  string
	Impact       string
	Remediation  string
	Reproduction string
	Status       string
	Verified     bool
	CreatedAt    domain.TimestampMs
	UpdatedAt    domain.TimestampMs
}

// FindingRepo reads and writes findings.
type FindingRepo struct{ db *DB }

// NewFindingRepo returns a repository over the shared database handle.
func NewFindingRepo(db *DB) *FindingRepo { return &FindingRepo{db: db} }

const findingColumns = `id, engagement_id, stage_run_id, title, category,
	severity, confidence, cvss_vector, cvss_score, asset_id, endpoint, parameter,
	description, impact, remediation, reproduction, status, verified,
	created_at, updated_at`

// severityRankSQL ranks severities explicitly. A lexical sort would put high
// before medium and would rank critical only by accident of spelling.
const severityRankSQL = `CASE severity
	WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3
	WHEN 'low' THEN 4 WHEN 'info' THEN 5 ELSE 6 END`

// Create persists one finding, minting its id when the caller left it unset.
func (r *FindingRepo) Create(ctx context.Context, finding Finding) (Finding, error) {
	if err := normalizeFinding(&finding); err != nil {
		return Finding{}, err
	}
	const query = `INSERT INTO findings (` + findingColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING ` + findingColumns
	row := r.db.Writer().QueryRowContext(ctx, query,
		finding.ID.String(), finding.EngagementID.String(), nullID(finding.StageRunID),
		finding.Title, finding.Category, finding.Severity, finding.Confidence,
		nullString(finding.CVSSVector), finding.CVSSScore, nullID(finding.AssetID),
		nullString(finding.Endpoint), nullString(finding.Parameter), finding.Description,
		nullString(finding.Impact), nullString(finding.Remediation),
		nullString(finding.Reproduction), finding.Status, boolToInt(finding.Verified),
		int64(finding.CreatedAt), int64(finding.UpdatedAt))
	stored, err := scanFinding(row)
	if err != nil {
		return Finding{}, fmt.Errorf("create finding %q: %w", finding.Title, err)
	}
	return stored, nil
}

// List returns an engagement's findings, most severe first then by title.
func (r *FindingRepo) List(ctx context.Context, engagementID domain.ID) ([]Finding, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("list findings: engagement id is empty: %w", ErrInvalidFinding)
	}
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+findingColumns+`
		FROM findings WHERE engagement_id = ?
		ORDER BY `+severityRankSQL+` ASC, title ASC, id ASC`, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list findings for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	return queryAll(rows, scanFinding, "finding")
}

// SetStatus moves one finding to status and stamps updated_at.
func (r *FindingRepo) SetStatus(ctx context.Context, id domain.ID, status string, at domain.TimestampMs) error {
	if id.IsZero() {
		return fmt.Errorf("%w: id is empty", ErrInvalidFinding)
	}
	if !slices.Contains(findingStatuses, status) {
		return fmt.Errorf("%w: status %q", ErrInvalidFinding, status)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: updated_at is empty", ErrInvalidFinding)
	}
	result, err := r.db.Writer().ExecContext(ctx,
		`UPDATE findings SET status = ?, updated_at = ? WHERE id = ?`,
		status, int64(at), id.String())
	if err != nil {
		return fmt.Errorf("set finding %q status %q: %w", id, status, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set finding %q status %q: %w", id, status, err)
	}
	if affected == 0 {
		return fmt.Errorf("finding %q: %w", id, ErrNotFound)
	}
	return nil
}

// Count returns how many findings an engagement has.
func (r *FindingRepo) Count(ctx context.Context, engagementID domain.ID) (int, error) {
	if engagementID.IsZero() {
		return 0, fmt.Errorf("count findings: engagement id is empty: %w", ErrInvalidFinding)
	}
	var total int
	err := r.db.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM findings WHERE engagement_id = ?`, engagementID.String()).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count findings for engagement %q: %w", engagementID, err)
	}
	return total, nil
}

// CountBySeverity returns a count for every severity level, zero included.
func (r *FindingRepo) CountBySeverity(ctx context.Context, engagementID domain.ID) (map[string]int, error) {
	if engagementID.IsZero() {
		return nil, fmt.Errorf("count findings: engagement id is empty: %w", ErrInvalidFinding)
	}
	counts := make(map[string]int, len(findingSeverities))
	for _, severity := range findingSeverities {
		counts[severity] = 0
	}
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT severity, COUNT(*) FROM findings WHERE engagement_id = ? GROUP BY severity`,
		engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("count findings by severity for engagement %q: %w", engagementID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var severity string
		var total int
		if err := rows.Scan(&severity, &total); err != nil {
			return nil, fmt.Errorf("scan severity count: %w", err)
		}
		counts[severity] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate severity counts: %w", err)
	}
	return counts, nil
}

func normalizeFinding(finding *Finding) error {
	if finding.EngagementID.IsZero() {
		return fmt.Errorf("%w: engagement_id is empty", ErrInvalidFinding)
	}
	if strings.TrimSpace(finding.Title) == "" {
		return fmt.Errorf("%w: title is empty", ErrInvalidFinding)
	}
	if !IsFindingCategory(finding.Category) {
		return fmt.Errorf("%w: category %q", ErrInvalidFinding, finding.Category)
	}
	if !slices.Contains(findingSeverities, finding.Severity) {
		return fmt.Errorf("%w: severity %q", ErrInvalidFinding, finding.Severity)
	}
	if !slices.Contains(findingConfidences, finding.Confidence) {
		return fmt.Errorf("%w: confidence %q", ErrInvalidFinding, finding.Confidence)
	}
	if strings.TrimSpace(finding.Description) == "" {
		return fmt.Errorf("%w: description is empty", ErrInvalidFinding)
	}
	if finding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is empty", ErrInvalidFinding)
	}
	if finding.CVSSScore != nil && (*finding.CVSSScore < 0 || *finding.CVSSScore > 10) {
		return fmt.Errorf("%w: cvss_score %v is outside 0..10", ErrInvalidFinding, *finding.CVSSScore)
	}
	if finding.Status == "" {
		finding.Status = "open"
	}
	if !slices.Contains(findingStatuses, finding.Status) {
		return fmt.Errorf("%w: status %q", ErrInvalidFinding, finding.Status)
	}
	if finding.UpdatedAt.IsZero() {
		finding.UpdatedAt = finding.CreatedAt
	}
	if finding.ID.IsZero() {
		finding.ID = rowIDMint.New()
	}
	return nil
}

func scanFinding(scanner rowScanner) (Finding, error) {
	var (
		finding      Finding
		id, engID    string
		stageRunID   *string
		cvssVector   *string
		cvssScore    *float64
		assetID      *string
		endpoint     *string
		parameter    *string
		impact       *string
		remediation  *string
		reproduction *string
		verified     int
		createdAt    int64
		updatedAt    int64
	)
	if err := scanner.Scan(&id, &engID, &stageRunID, &finding.Title, &finding.Category,
		&finding.Severity, &finding.Confidence, &cvssVector, &cvssScore, &assetID,
		&endpoint, &parameter, &finding.Description, &impact, &remediation,
		&reproduction, &finding.Status, &verified, &createdAt, &updatedAt); err != nil {
		return Finding{}, err
	}
	var err error
	if finding.ID, err = domain.NewID(id); err != nil {
		return Finding{}, fmt.Errorf("finding id: %w", err)
	}
	if finding.EngagementID, err = domain.NewID(engID); err != nil {
		return Finding{}, fmt.Errorf("finding engagement id: %w", err)
	}
	finding.CVSSVector = derefString(cvssVector)
	finding.CVSSScore = cvssScore
	finding.Endpoint = derefString(endpoint)
	finding.Parameter = derefString(parameter)
	finding.Impact = derefString(impact)
	finding.Remediation = derefString(remediation)
	finding.Reproduction = derefString(reproduction)
	finding.Verified = verified == 1
	finding.CreatedAt = domain.TimestampMs(createdAt)
	finding.UpdatedAt = domain.TimestampMs(updatedAt)
	if stageRunID != nil && *stageRunID != "" {
		if finding.StageRunID, err = domain.NewID(*stageRunID); err != nil {
			return Finding{}, fmt.Errorf("finding stage run id: %w", err)
		}
	}
	if assetID != nil && *assetID != "" {
		if finding.AssetID, err = domain.NewID(*assetID); err != nil {
			return Finding{}, fmt.Errorf("finding asset id: %w", err)
		}
	}
	return finding, nil
}
