package osint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// CRTSh queries the Certificate Transparency log aggregator at crt.sh.
//
// Plain HTTPS rather than a container execution: crt.sh is a public read-only
// index and the query needs no tooling beyond the standard library, so routing
// it through the Docker executor would add latency and an image dependency for
// no isolation benefit. The target host is never contacted.
type CRTSh struct {
	client   HTTPDoer
	settings Settings
}

// NewCRTSh returns a crt.sh adapter over client.
func NewCRTSh(client HTTPDoer, opts ...Option) *CRTSh {
	return &CRTSh{client: client, settings: newSettings("https://crt.sh", opts)}
}

// Name implements Source.
func (c *CRTSh) Name() string { return "crtsh" }

type crtShEntry struct {
	NameValue string `json:"name_value"`
}

// Query returns one Record per unique subdomain seen in the certificate log.
func (c *CRTSh) Query(ctx context.Context, target string) ([]Record, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, fmt.Errorf("crtsh query: %w", err)
	}

	endpoint := c.settings.BaseURL + "/?q=" + url.QueryEscape("%."+strings.ToLower(target)) + "&output=json"
	body, err := fetch(ctx, c.client, endpoint, c.settings.Timeout)
	if err != nil {
		return nil, fmt.Errorf("crtsh query %q: %w", target, err)
	}

	var entries []crtShEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("crtsh query %q: decode json: %w: %w", target, ErrBadResponse, err)
	}

	seen := make(map[string]bool, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		for _, raw := range strings.Split(entry.NameValue, "\n") {
			name := strings.ToLower(strings.TrimSpace(raw))
			name = strings.TrimPrefix(name, "*.")
			if name == "" || strings.Contains(name, "*") || !isHostname(name) {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	if len(names) > c.settings.MaxRecords {
		names = names[:c.settings.MaxRecords]
	}

	records := make([]Record, 0, len(names))
	for _, name := range names {
		records = append(records, Record{
			Kind:       "subdomain",
			Value:      name,
			Source:     c.Name(),
			Confidence: 0.9,
			Attrs:      map[string]string{"discovered_via": "certificate_transparency"},
		})
	}
	return records, nil
}

// isHostname reports whether value is a dot-separated name of DNS-label runes.
func isHostname(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
