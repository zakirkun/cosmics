// Package report renders an engagement as a document.
//
// Three constraints shape it. Every ordering is explicit, because two runs over the
// same database must produce byte-identical output. The timestamp is supplied by
// the caller, never read from the wall clock, for the same reason. And a secret
// can never reach a rendered document: the model holds credential references, not
// values, and a redaction pass runs over the final text as a second line.
package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Metric values of the CVSS v3.1 base vector.
//
// v3.1 is implemented rather than v4.0 because v4.0 scores from a 270-row
// macrovector lookup table. Reproducing that table from memory would put wrong
// severities into every report, which is worse than not scoring at all. The v4.0
// vector is still stored verbatim, so a later scorer can compute it without any
// data loss.
const (
	attackNetwork  = "N"
	attackAdjacent = "A"
	attackLocal    = "L"
	attackPhysical = "P"

	complexityLow  = "L"
	complexityHigh = "H"

	privilegeNone = "N"
	privilegeLow  = "L"
	privilegeHigh = "H"

	userInteractionNone     = "N"
	userInteractionRequired = "R"

	impactHigh = "H"
	impactLow  = "L"
	impactNone = "N"

	scopeUnchanged = "U"
	scopeChanged   = "C"
)

// Vector is a parsed CVSS v3.1 base vector.
type Vector struct {
	AttackVector       string
	AttackComplexity   string
	PrivilegesRequired string
	UserInteraction    string
	Scope              string
	Confidentiality    string
	Integrity          string
	Availability       string
}

// Sentinel errors for vector parsing.
var (
	ErrInvalidVector      = fmt.Errorf("report: invalid CVSS vector")
	ErrUnsupportedVersion = fmt.Errorf("report: unsupported CVSS version")
)

// ParseVector parses a CVSS v3.1 base vector such as
// CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H.
func ParseVector(raw string) (Vector, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return Vector{}, fmt.Errorf("vector is empty: %w", ErrInvalidVector)
	}

	parts := strings.Split(text, "/")
	if len(parts) < 2 {
		return Vector{}, fmt.Errorf("vector %q has no metrics: %w", raw, ErrInvalidVector)
	}

	switch parts[0] {
	case "CVSS:3.1", "CVSS:3.0":
	case "CVSS:4.0":
		return Vector{}, fmt.Errorf("vector %q is v4.0; this scorer implements 3.1: %w",
			raw, ErrUnsupportedVersion)
	default:
		return Vector{}, fmt.Errorf("vector %q has unknown prefix %q: %w",
			raw, parts[0], ErrInvalidVector)
	}

	values := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		key, value, found := strings.Cut(part, ":")
		if !found || key == "" || value == "" {
			return Vector{}, fmt.Errorf("vector %q has malformed metric %q: %w",
				raw, part, ErrInvalidVector)
		}
		if _, duplicate := values[key]; duplicate {
			return Vector{}, fmt.Errorf("vector %q repeats metric %q: %w",
				raw, key, ErrInvalidVector)
		}
		values[key] = value
	}

	vector := Vector{
		AttackVector:       values["AV"],
		AttackComplexity:   values["AC"],
		PrivilegesRequired: values["PR"],
		UserInteraction:    values["UI"],
		Scope:              values["S"],
		Confidentiality:    values["C"],
		Integrity:          values["I"],
		Availability:       values["A"],
	}

	if err := vector.validate(raw); err != nil {
		return Vector{}, err
	}
	return vector, nil
}

// validate checks every metric against its permitted values.
func (v Vector) validate(raw string) error {
	checks := []struct {
		name    string
		value   string
		allowed []string
	}{
		{"AV", v.AttackVector, []string{attackNetwork, attackAdjacent, attackLocal, attackPhysical}},
		{"AC", v.AttackComplexity, []string{complexityLow, complexityHigh}},
		{"PR", v.PrivilegesRequired, []string{privilegeNone, privilegeLow, privilegeHigh}},
		{"UI", v.UserInteraction, []string{userInteractionNone, userInteractionRequired}},
		{"S", v.Scope, []string{scopeUnchanged, scopeChanged}},
		{"C", v.Confidentiality, []string{impactHigh, impactLow, impactNone}},
		{"I", v.Integrity, []string{impactHigh, impactLow, impactNone}},
		{"A", v.Availability, []string{impactHigh, impactLow, impactNone}},
	}

	for _, check := range checks {
		if check.value == "" {
			return fmt.Errorf("vector %q is missing %s: %w", raw, check.name, ErrInvalidVector)
		}
		valid := false
		for _, allowed := range check.allowed {
			if check.value == allowed {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("vector %q has %s:%s which is not a permitted value: %w",
				raw, check.name, check.value, ErrInvalidVector)
		}
	}
	return nil
}

// BaseScore computes the CVSS v3.1 base score.
//
// The formula is section 7.1 of the v3.1 specification: the impact sub-score is
// combined with exploitability and then, only for a changed scope, raised by a
// factor of 1.08 before the round-up. The round-up is specified to one decimal and
// must not be plain rounding - 4.02 must become 4.1, not 4.0.
func (v Vector) BaseScore() (float64, error) {
	if err := v.validate(""); err != nil {
		return 0, err
	}

	scopeChanged := v.Scope == scopeChanged

	iss := 1 - ((1 - impactWeight(v.Confidentiality)) *
		(1 - impactWeight(v.Integrity)) *
		(1 - impactWeight(v.Availability)))

	var impact float64
	if scopeChanged {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}

	exploitability := 8.22 *
		attackVectorWeight(v.AttackVector) *
		attackComplexityWeight(v.AttackComplexity) *
		privilegeWeight(v.PrivilegesRequired, scopeChanged) *
		userInteractionWeight(v.UserInteraction)

	if impact <= 0 {
		return 0, nil
	}

	if scopeChanged {
		return roundUp(math.Min(1.08*(impact+exploitability), 10)), nil
	}
	return roundUp(math.Min(impact+exploitability, 10)), nil
}

// roundUp rounds up to one decimal place as the specification defines it.
func roundUp(value float64) float64 {
	scaled := int(math.Round(value * 100000))
	if scaled%10000 == 0 {
		return float64(scaled) / 100000
	}
	return (math.Floor(float64(scaled)/10000) + 1) / 10
}

func impactWeight(value string) float64 {
	switch value {
	case impactHigh:
		return 0.56
	case impactLow:
		return 0.22
	case impactNone:
		return 0
	}
	return 0
}

func attackVectorWeight(value string) float64 {
	switch value {
	case attackNetwork:
		return 0.85
	case attackAdjacent:
		return 0.62
	case attackLocal:
		return 0.55
	case attackPhysical:
		return 0.2
	}
	return 0
}

func attackComplexityWeight(value string) float64 {
	if value == complexityLow {
		return 0.77
	}
	return 0.44
}

func privilegeWeight(value string, scopeChanged bool) float64 {
	switch value {
	case privilegeNone:
		return 0.85
	case privilegeLow:
		if scopeChanged {
			return 0.68
		}
		return 0.62
	case privilegeHigh:
		if scopeChanged {
			return 0.5
		}
		return 0.27
	}
	return 0
}

func userInteractionWeight(value string) float64 {
	if value == userInteractionNone {
		return 0.85
	}
	return 0.62
}

// SeverityFromScore maps a base score to its qualitative band.
func SeverityFromScore(score float64) string {
	switch {
	case score >= 9.0:
		return "critical"
	case score >= 7.0:
		return "high"
	case score >= 4.0:
		return "medium"
	case score > 0:
		return "low"
	}
	return "info"
}

// FormatScore renders a score the way a report shows it.
func FormatScore(score float64) string {
	return strconv.FormatFloat(score, 'f', 1, 64)
}
