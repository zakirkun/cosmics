// Package clock provides the production implementation of domain.Clock.
// Domain code never calls time.Now directly; it receives a Clock so that
// timestamps are injectable and tests stay deterministic.
package clock

import (
	"sync"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
)

// System reads the host clock in UTC.
type System struct{}

// NewSystem returns a clock backed by the operating system time source.
func NewSystem() System { return System{} }

// Now returns the current instant truncated to milliseconds, which is the
// resolution the database stores.
func (System) Now() domain.TimestampMs {
	return domain.TimestampMs(time.Now().UTC().UnixMilli())
}

// Fixed always returns the same instant. Tests use it to make timestamp
// assertions exact.
type Fixed struct {
	mu  sync.Mutex
	now domain.TimestampMs
}

// NewFixed returns a clock pinned to ts.
func NewFixed(ts domain.TimestampMs) *Fixed { return &Fixed{now: ts} }

// Now returns the pinned instant.
func (f *Fixed) Now() domain.TimestampMs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the pinned instant forward by d. Tests use it to simulate the
// passage of time without sleeping.
func (f *Fixed) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now += domain.TimestampMs(d.Milliseconds())
}

// Set pins the clock to ts.
func (f *Fixed) Set(ts domain.TimestampMs) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = ts
}
