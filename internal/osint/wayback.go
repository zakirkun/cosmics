package osint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Wayback queries the Internet Archive's CDX index for archived URLs.
//
// Plain HTTP/HTTPS rather than a container execution: the CDX endpoint is a
// public read-only index and the adapter only parses JSON, so a container adds
// start-up cost and an image dependency without any isolation benefit. The
// target host is never contacted.
type Wayback struct {
	client   HTTPDoer
	settings Settings
}

// NewWayback returns a Wayback adapter over client.
func NewWayback(client HTTPDoer, opts ...Option) *Wayback {
	settings := newSettings("http://web.archive.org", opts)
	if settings.MaxRecords > 1000 {
		settings.MaxRecords = 1000
	}
	return &Wayback{client: client, settings: settings}
}

// Name implements Source.
func (w *Wayback) Name() string { return "wayback" }

// Query returns one Record per unique archived URL below the target.
func (w *Wayback) Query(ctx context.Context, target string) ([]Record, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, fmt.Errorf("wayback query: %w", err)
	}

	endpoint := w.settings.BaseURL + "/cdx/search/cdx?url=" +
		url.QueryEscape(strings.ToLower(target)+"/*") +
		"&output=json&collapse=urlkey&fl=original&limit=" + fmt.Sprint(w.settings.MaxRecords)

	body, err := fetch(ctx, w.client, endpoint, w.settings.Timeout)
	if err != nil {
		return nil, fmt.Errorf("wayback query %q: %w", target, err)
	}

	rows, err := decodeCDX(body)
	if err != nil {
		return nil, fmt.Errorf("wayback query %q: %w: %w", target, ErrBadResponse, err)
	}

	seen := make(map[string]bool, len(rows))
	urls := make([]string, 0, len(rows))
	for _, row := range rows {
		original := strings.TrimSpace(row)
		if original == "" || seen[original] {
			continue
		}
		if _, parseErr := url.Parse(original); parseErr != nil {
			continue
		}
		seen[original] = true
		urls = append(urls, original)
	}
	sort.Strings(urls)

	if len(urls) > w.settings.MaxRecords {
		urls = urls[:w.settings.MaxRecords]
	}

	records := make([]Record, 0, len(urls))
	for _, archived := range urls {
		records = append(records, Record{
			Kind:       "url",
			Value:      archived,
			Source:     w.Name(),
			Confidence: 0.6,
			Attrs:      map[string]string{"discovered_via": "wayback_cdx"},
		})
	}
	return records, nil
}

// decodeCDX parses the CDX JSON array, which carries a header row first.
//
// A single-row response is the header alone, i.e. no captures; a zero-length
// array is an empty result for an unknown domain. Both are valid and yield no
// rows, so the header is dropped by position rather than by comparing text.
func decodeCDX(body []byte) ([]string, error) {
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode cdx json: %w", err)
	}
	if len(rows) <= 1 {
		return nil, nil
	}

	original := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		original = append(original, row[0])
	}
	return original, nil
}
