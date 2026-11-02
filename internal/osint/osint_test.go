package osint_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/osint"
)

// newTestServer serves one canned body at every path and returns a client
// pointed at it, so no adapter ever reaches the real internet in a test.
func newTestServer(t *testing.T, body string) (*httptest.Server, *http.Client) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server, server.Client()
}

func Test_crtsh_returns_unique_subdomains_when_the_response_has_duplicates(t *testing.T) {
	// Given
	body := `[
		{"name_value":"a.example.test"},
		{"name_value":"a.example.test\nb.example.test"},
		{"name_value":"B.EXAMPLE.TEST"},
		{"name_value":"c.example.test"}
	]`
	server, client := newTestServer(t, body)
	source := osint.NewCRTSh(client, osint.WithBaseURL(server.URL))

	// When
	records, err := source.Query(context.Background(), "example.test")

	// Then
	require.NoError(t, err)
	require.Len(t, records, 3, "duplicates and case variants must collapse")
	values := []string{records[0].Value, records[1].Value, records[2].Value}
	assert.Equal(t, []string{"a.example.test", "b.example.test", "c.example.test"}, values,
		"output must be sorted and lowercased")
	for _, record := range records {
		assert.Equal(t, "subdomain", record.Kind)
		assert.Equal(t, "crtsh", record.Source)
		assert.InDelta(t, 0.9, record.Confidence, 1e-9)
	}
}

func Test_crtsh_drops_wildcards_when_the_name_value_contains_one(t *testing.T) {
	// Given
	body := `[{"name_value":"*.example.test"},{"name_value":"api.example.test"}]`
	server, client := newTestServer(t, body)
	source := osint.NewCRTSh(client, osint.WithBaseURL(server.URL))

	// When
	records, err := source.Query(context.Background(), "example.test")

	// Then
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "api.example.test", records[0].Value)
	assert.Equal(t, "example.test", records[1].Value,
		"a leading wildcard label is stripped, the remaining name is kept")
	for _, record := range records {
		assert.NotContains(t, record.Value, "*")
	}
}

func Test_crtsh_returns_an_error_when_the_response_is_not_json(t *testing.T) {
	// Given
	server, client := newTestServer(t, "<html>rate limited</html>")
	source := osint.NewCRTSh(client, osint.WithBaseURL(server.URL))

	// When
	records, err := source.Query(context.Background(), "example.test")

	// Then
	require.Error(t, err)
	assert.Nil(t, records)
	require.ErrorIs(t, err, osint.ErrBadResponse)
}

// fakeResolver returns a canned answer, so the DNS adapter never resolves.
type fakeResolver struct {
	addresses []string
	err       error
}

func (r fakeResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	return r.addresses, r.err
}

func Test_dns_returns_one_record_per_address_when_the_resolver_finds_several(t *testing.T) {
	// Given
	source := osint.NewDNS(fakeResolver{addresses: []string{
		"203.0.113.10", "2001:db8::1", "203.0.113.10",
	}})

	// When
	records, err := source.Query(context.Background(), "example.test")

	// Then
	require.NoError(t, err)
	require.Len(t, records, 2, "the duplicate address must collapse")
	assert.Equal(t, "2001:db8::1", records[0].Value)
	assert.Equal(t, "203.0.113.10", records[1].Value)
	for _, record := range records {
		assert.Equal(t, "ip", record.Kind)
		assert.Equal(t, "dns", record.Source)
		assert.InDelta(t, 1.0, record.Confidence, 1e-9)
	}
}

func Test_dns_returns_no_records_when_the_host_does_not_resolve(t *testing.T) {
	// Given
	source := osint.NewDNS(fakeResolver{
		err: &net.DNSError{Err: "no such host", Name: "missing.example.test", IsNotFound: true},
	})

	// When
	records, err := source.Query(context.Background(), "missing.example.test")

	// Then
	require.NoError(t, err, "NXDOMAIN is a fact about the target, not a stage failure")
	assert.Empty(t, records)
}

func Test_wayback_skips_the_header_row_when_the_cdx_response_is_parsed(t *testing.T) {
	// Given
	body := `[["original"],["https://example.test/a"],["https://example.test/b"],["https://example.test/a"]]`
	server, client := newTestServer(t, body)
	source := osint.NewWayback(client, osint.WithBaseURL(server.URL))

	// When
	records, err := source.Query(context.Background(), "example.test")

	// Then
	require.NoError(t, err)
	require.Len(t, records, 2, "the header row and the duplicate must both be dropped")
	assert.Equal(t, "https://example.test/a", records[0].Value)
	assert.Equal(t, "https://example.test/b", records[1].Value)
	for _, record := range records {
		assert.Equal(t, "url", record.Kind)
		assert.NotEqual(t, "original", record.Value)
	}
}

func Test_rdap_extracts_registrar_and_dates_when_the_response_is_valid(t *testing.T) {
	// Given
	body := `{
		"events": [
			{"eventAction":"registration","eventDate":"2001-02-03T04:05:06Z"},
			{"eventAction":"expiration","eventDate":"2030-02-03T04:05:06Z"}
		],
		"entities": [{
			"roles": ["registrar"],
			"vcardArray": ["vcard", [["fn", {}, "text", "Example Registrar Inc."]]]
		}]
	}`
	server, client := newTestServer(t, body)
	source := osint.NewRDAP(client, osint.WithBaseURL(server.URL))

	// When
	records, err := source.Query(context.Background(), "Example.Test")

	// Then
	require.NoError(t, err)
	require.Len(t, records, 1, "RDAP yields exactly one domain record")
	record := records[0]
	assert.Equal(t, "domain", record.Kind)
	assert.Equal(t, "example.test", record.Value)
	assert.Equal(t, "Example Registrar Inc.", record.Attrs["registrar"])
	assert.Equal(t, "2001-02-03T04:05:06Z", record.Attrs["created"])
	assert.Equal(t, "2030-02-03T04:05:06Z", record.Attrs["expires"])
}

// namedSource is a registry entry that returns nothing.
type namedSource struct{ name string }

func (s namedSource) Name() string { return s.name }

func (s namedSource) Query(context.Context, string) ([]osint.Record, error) {
	return nil, nil
}

func Test_Registry_Enabled_filters_to_the_named_sources_when_given_a_list(t *testing.T) {
	// Given
	registry := osint.NewRegistry(
		namedSource{name: "alpha"}, namedSource{name: "beta"}, namedSource{name: "gamma"},
	)

	// When
	enabled := registry.Enabled([]string{"gamma", "alpha", "does-not-exist"})

	// Then
	assert.Equal(t, []string{"alpha", "gamma"}, enabled.Names(),
		"only the named sources survive, sorted by name; an unknown name is skipped")
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, registry.Names(),
		"the original registry is unchanged")
}

func Test_Registry_Get_returns_ErrUnknownSource_when_absent(t *testing.T) {
	// Given
	registry := osint.NewRegistry(namedSource{name: "alpha"})

	// When
	source, err := registry.Get("missing")

	// Then
	require.Error(t, err)
	assert.Nil(t, source)
	require.ErrorIs(t, err, osint.ErrUnknownSource)
	assert.Contains(t, err.Error(), "alpha", "the error must list what is registered")
}

func Test_ValidateTarget_rejects_shell_metacharacters(t *testing.T) {
	for _, target := range []string{
		"evil.com; rm -rf /", "evil.com && whoami", "evil.com|nc", "$(id).com", "a`b`.com",
	} {
		t.Run(target, func(t *testing.T) {
			assert.ErrorIs(t, osint.ValidateTarget(target), osint.ErrInvalidTarget)
		})
	}
	assert.NoError(t, osint.ValidateTarget("example.test"))
}

var _ osint.HTTPDoer = (*http.Client)(nil)
