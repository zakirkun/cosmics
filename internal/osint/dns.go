package osint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
)

// Resolver is the narrow DNS surface the adapter needs, so a test supplies a
// fake without reaching the network.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// NetResolver resolves through the host's configured resolver.
type NetResolver struct {
	resolver *net.Resolver
}

// NewNetResolver returns a resolver backed by net.DefaultResolver.
func NewNetResolver() *NetResolver { return &NetResolver{resolver: net.DefaultResolver} }

// LookupHost implements Resolver.
func (n *NetResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return n.resolver.LookupHost(ctx, host)
}

// DNS resolves a target's A and AAAA records.
type DNS struct {
	resolver Resolver
}

// NewDNS returns a DNS adapter over resolver.
func NewDNS(resolver Resolver) *DNS { return &DNS{resolver: resolver} }

// Name implements Source.
func (d *DNS) Name() string { return "dns" }

// Query returns one Record per distinct address the resolver reports.
//
// A host that does not resolve is not an error: NXDOMAIN is a fact about the
// target, and the OSINT stage records it as an empty result rather than failing
// the whole stage. Only a resolver failure that is not a not-found condition is
// propagated.
func (d *DNS) Query(ctx context.Context, target string) ([]Record, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, fmt.Errorf("dns query: %w", err)
	}
	if d.resolver == nil {
		return nil, fmt.Errorf("dns query %q: %w", target, ErrNoClient)
	}

	addresses, err := d.resolver.LookupHost(ctx, target)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("dns query %q: %w", target, err)
	}

	seen := make(map[string]bool, len(addresses))
	unique := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parsed := net.ParseIP(address)
		if parsed == nil || seen[address] {
			continue
		}
		seen[address] = true
		unique = append(unique, address)
	}
	sort.Strings(unique)

	records := make([]Record, 0, len(unique))
	for _, address := range unique {
		records = append(records, Record{
			Kind:       "ip",
			Value:      address,
			Source:     d.Name(),
			Confidence: 1.0,
			Attrs:      map[string]string{"resolved_from": target},
		})
	}
	return records, nil
}

// isNotFound reports whether a resolver error means the name does not exist.
func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}
