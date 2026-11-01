package domain

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// ulidParse validates a ULID string, returning the parsed value.
func ulidParse(s string) (ulid.ULID, error) {
	return ulid.ParseStrict(s)
}

// ULIDGenerator mints monotonic ULIDs. Monotonicity guarantees that two
// identifiers created in the same millisecond still sort in creation order,
// which the append-only tables rely on for stable ordering.
type ULIDGenerator struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// NewULIDGenerator returns a generator seeded from crypto/rand.
func NewULIDGenerator() *ULIDGenerator {
	return &ULIDGenerator{entropy: ulid.Monotonic(rand.Reader, 0)}
}

// New mints the next identifier. It is safe for concurrent use.
func (g *ULIDGenerator) New() ID {
	return g.NewAt(TimestampMs(time.Now().UTC().UnixMilli()))
}

// NewAt mints an identifier with an explicit timestamp. Tests use it to make
// ordering assertions deterministic.
func (g *ULIDGenerator) NewAt(ts TimestampMs) ID {
	g.mu.Lock()
	defer g.mu.Unlock()

	return ID{raw: ulid.MustNew(ulid.Timestamp(ts.Time()), g.entropy).String()}
}
