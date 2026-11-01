package memory

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanFact maps one factColumns row into a Fact.
func scanFact(s scanner) (Fact, error) {
	var (
		fact               Fact
		id, engagementID   string
		subject, predicate string
		value, valueType   string
		confidence         float64
		sourceKind         sql.NullString
		sourceID           sql.NullString
		stage              string
		supersededBy       sql.NullString
		createdAt          int64
	)

	if err := s.Scan(&id, &engagementID, &subject, &predicate, &value, &valueType,
		&confidence, &sourceKind, &sourceID, &stage, &supersededBy, &createdAt); err != nil {
		return Fact{}, err
	}

	parsedID, err := domain.NewID(id)
	if err != nil {
		return Fact{}, fmt.Errorf("fact id: %w", err)
	}
	parsedEngagementID, err := domain.NewID(engagementID)
	if err != nil {
		return Fact{}, fmt.Errorf("fact engagement id: %w", err)
	}
	if supersededBy.Valid && supersededBy.String != "" {
		if fact.SupersededBy, err = domain.NewID(supersededBy.String); err != nil {
			return Fact{}, fmt.Errorf("fact superseded_by: %w", err)
		}
	}

	fact.ID = parsedID
	fact.EngagementID = parsedEngagementID
	fact.Subject = subject
	fact.Predicate = predicate
	fact.Value = value
	fact.ValueType = valueType
	fact.Confidence = confidence
	fact.SourceKind = sourceKind.String
	fact.SourceID = sourceID.String
	fact.Stage = domain.StageKind(stage)
	fact.CreatedAt = domain.TimestampMs(createdAt)
	return fact, nil
}

// validate rejects anything the facts table CHECK constraints would reject, so
// the caller sees a typed error before a transaction is opened.
func (p SetParams) validate() error {
	checks := []struct {
		field string
		ok    bool
		value string
	}{
		{"engagement id", !p.EngagementID.IsZero(), p.EngagementID.String()},
		{"subject", strings.TrimSpace(p.Subject) != "", p.Subject},
		{"predicate", strings.TrimSpace(p.Predicate) != "", p.Predicate},
		{"value", strings.TrimSpace(p.Value) != "", p.Value},
		{"confidence", p.Confidence >= 0 && p.Confidence <= 1, fmt.Sprintf("%v", p.Confidence)},
		{"value_type", validValueType(p.ValueType), p.ValueType},
		{"source_kind", validSourceKind(p.SourceKind), p.SourceKind},
		{"stage", p.Stage.Valid(), string(p.Stage)},
		{"now", !p.Now.IsZero(), p.Now.Time().String()},
	}
	for _, check := range checks {
		if !check.ok {
			return fmt.Errorf("set fact %s[%s]: invalid %s %q: %w",
				p.Subject, p.Predicate, check.field, check.value, ErrInvalidFact)
		}
	}
	return nil
}

// validValueType reports whether vt is in the facts.value_type CHECK list.
func validValueType(vt string) bool {
	switch vt {
	case ValueTypeText, ValueTypeNumber, ValueTypeBool, ValueTypeJSON, ValueTypeRef:
		return true
	}
	return false
}

// validSourceKind reports whether sk is in the facts.source_kind CHECK list.
func validSourceKind(sk string) bool {
	switch sk {
	case SourceToolRun, SourceLLMInference, SourceOperator, SourceOSINT:
		return true
	}
	return false
}

// nullText stores SQL NULL for an unset optional string.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
