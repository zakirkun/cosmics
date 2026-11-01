package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/clock"
	"github.com/cosmics/cosmics/internal/domain"
)

func Test_Fixed_Now_returns_the_pinned_instant_when_first_constructed(t *testing.T) {
	// Given
	const pinned = domain.TimestampMs(1735689600123)
	c := clock.NewFixed(pinned)

	// When
	got := c.Now()

	// Then
	require.Equal(t, pinned, got)
}

func Test_Fixed_Now_is_stable_when_called_repeatedly(t *testing.T) {
	// Given
	const pinned = domain.TimestampMs(1735689600000)
	c := clock.NewFixed(pinned)

	// When
	first := c.Now()
	second := c.Now()

	// Then
	require.Equal(t, first, second)
}

func Test_Fixed_Now_advances_by_the_exact_duration_when_advanced(t *testing.T) {
	// Given
	const pinned = domain.TimestampMs(1735689600000)
	c := clock.NewFixed(pinned)

	// When
	c.Advance(1500 * time.Millisecond)

	// Then
	require.Equal(t, pinned+1500, c.Now())
}

func Test_Fixed_Now_returns_the_new_value_after_set(t *testing.T) {
	// Given
	c := clock.NewFixed(1)
	const replacement = domain.TimestampMs(999999)

	// When
	c.Set(replacement)

	// Then
	require.Equal(t, replacement, c.Now())
}

func Test_Fixed_advance_is_safe_for_concurrent_use(t *testing.T) {
	// Given
	c := clock.NewFixed(0)
	const workers = 8
	const increments = 50

	// When
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range increments {
				c.Advance(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// Then
	require.Equal(t, domain.TimestampMs(workers*increments), c.Now())
}

func Test_System_Now_returns_a_plausible_present_instant(t *testing.T) {
	// Given
	c := clock.NewSystem()
	before := domain.TimestampMs(time.Now().UTC().UnixMilli())

	// When
	got := c.Now()

	// Then
	after := domain.TimestampMs(time.Now().UTC().UnixMilli())
	assert.GreaterOrEqual(t, got, before)
	assert.LessOrEqual(t, got, after)
	assert.False(t, got.IsZero())
}

func Test_System_satisfies_the_domain_clock_contract(t *testing.T) {
	// Given
	var c domain.Clock = clock.NewSystem()

	// When
	got := c.Now()

	// Then
	assert.False(t, got.IsZero())
}
