package osint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// RDAP queries the Registration Data Access Protocol bootstrap service.
//
// Plain HTTPS rather than a container execution: RDAP is a public read-only
// registry query and the adapter only decodes JSON, so a container would add
// latency and an image dependency without an isolation benefit. The target host
// is never contacted — rdap.org resolves the authoritative registry itself.
type RDAP struct {
	client   HTTPDoer
	settings Settings
}

// NewRDAP returns an RDAP adapter over client.
func NewRDAP(client HTTPDoer, opts ...Option) *RDAP {
	return &RDAP{client: client, settings: newSettings("https://rdap.org", opts)}
}

// Name implements Source.
func (r *RDAP) Name() string { return "rdap" }

type rdapDomain struct {
	Events []struct {
		EventAction string `json:"eventAction"`
		EventDate   string `json:"eventDate"`
	} `json:"events"`
	Entities []struct {
		Roles      []string          `json:"roles"`
		VCardArray []json.RawMessage `json:"vcardArray"`
	} `json:"entities"`
}

// Query returns exactly one Record describing the registered domain.
//
// The registrar and the registration/expiry dates are attributes rather than
// separate records: they characterise the same asset, and splitting them would
// create findings with no independent meaning.
func (r *RDAP) Query(ctx context.Context, target string) ([]Record, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, fmt.Errorf("rdap query: %w", err)
	}

	endpoint := r.settings.BaseURL + "/domain/" + url.PathEscape(strings.ToLower(target))
	body, err := fetch(ctx, r.client, endpoint, r.settings.Timeout)
	if err != nil {
		return nil, fmt.Errorf("rdap query %q: %w", target, err)
	}

	var domain rdapDomain
	if err := json.Unmarshal(body, &domain); err != nil {
		return nil, fmt.Errorf("rdap query %q: decode json: %w: %w", target, ErrBadResponse, err)
	}

	attrs := map[string]string{"discovered_via": "rdap"}
	if registrar := registrarName(domain); registrar != "" {
		attrs["registrar"] = registrar
	}
	for _, event := range domain.Events {
		switch strings.ToLower(event.EventAction) {
		case "registration":
			if event.EventDate != "" {
				attrs["created"] = event.EventDate
			}
		case "expiration":
			if event.EventDate != "" {
				attrs["expires"] = event.EventDate
			}
		}
	}

	return []Record{{
		Kind:       "domain",
		Value:      strings.ToLower(target),
		Source:     r.Name(),
		Confidence: 1.0,
		Attrs:      attrs,
	}}, nil
}

// registrarName extracts "fn" from the registrar entity's jCard, if present.
func registrarName(domain rdapDomain) string {
	for _, entity := range domain.Entities {
		if !containsRole(entity.Roles, "registrar") {
			continue
		}
		for _, raw := range entity.VCardArray {
			var jcard []json.RawMessage
			if err := json.Unmarshal(raw, &jcard); err != nil {
				continue
			}
			if name := jcardProperty(jcard, "fn"); name != "" {
				return name
			}
		}
	}
	return ""
}

// jcardProperty returns the text value of one jCard property.
//
// A jCard is ["vcard", [ ["fn", {}, "text", "Name"], ... ]]: element 0 is the
// property name and element 3 its value. Anything shorter is not a property.
func jcardProperty(jcard []json.RawMessage, property string) string {
	for _, entry := range jcard {
		var fields []json.RawMessage
		if err := json.Unmarshal(entry, &fields); err != nil || len(fields) < 4 {
			continue
		}
		var name string
		if err := json.Unmarshal(fields[0], &name); err != nil || !strings.EqualFold(name, property) {
			continue
		}
		var value string
		if err := json.Unmarshal(fields[3], &value); err == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// containsRole reports whether roles includes want, case-insensitively.
func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), want) {
			return true
		}
	}
	return false
}
