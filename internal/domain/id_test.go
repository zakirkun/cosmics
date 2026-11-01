package domain_test

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
)

// Fixed instants. Domain tests never read the wall clock.
var (
	refTime = domain.TimestampMs(1735689600000) // 2025-01-01T00:00:00Z
	t1      = refTime + domain.TimestampMs(1_000)
	t2      = refTime + domain.TimestampMs(2_000)
	t3      = refTime + domain.TimestampMs(3_000)
	t4      = refTime + domain.TimestampMs(4_000)
)

// Valid Crockford base32 ULIDs used as fixtures across the package tests.
const (
	ulidA = "01ARZ3NDEKTSV4RRQH65QH65QH"
	ulidB = "01BX5ZZKBKACTAV9WEVGEMMVRY"
	ulidC = "01BX5ZZKBKACTAV9WEVGEMMVRZ"
)

// mustID parses s or fails the test.
func mustID(t *testing.T, s string) domain.ID {
	t.Helper()

	id, err := domain.NewID(s)
	require.NoError(t, err)
	return id
}

func Test_NewID_AcceptsValidULID_when_InputIsCanonical(t *testing.T) {
	// Given
	input := ulidA

	// When
	id, err := domain.NewID(input)

	// Then
	require.NoError(t, err)
	require.Equal(t, ulidA, id.String())
	require.False(t, id.IsZero())
	require.Len(t, id.String(), 26)
}

func Test_NewID_NormalizesToUppercase_when_InputIsLowercase(t *testing.T) {
	// Given
	const lower = "01arz3ndektsv4rrqh65qh65qh"

	// When
	id, err := domain.NewID(lower)

	// Then
	require.NoError(t, err)
	require.Equal(t, "01ARZ3NDEKTSV4RRQH65QH65QH", id.String())
}

func Test_NewID_TrimsSurroundingSpace_when_InputIsPadded(t *testing.T) {
	// Given
	input := "  \t" + ulidA + "\r\n "

	// When
	id, err := domain.NewID(input)

	// Then
	require.NoError(t, err)
	require.Equal(t, ulidA, id.String())
}

func Test_NewID_Rejects_when_InputIsMalformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "empty", in: "", want: domain.ErrEmptyID},
		{name: "whitespace only", in: " \t\r\n ", want: domain.ErrEmptyID},
		{name: "too short", in: ulidA[:25], want: domain.ErrInvalidID},
		{name: "too long", in: ulidA + "0", want: domain.ErrInvalidID},
		{name: "nil character", in: "01ARZ3NDEKTSV4RRQH65QH65Q\x00", want: domain.ErrInvalidID},
		{name: "excluded letter I", in: "01ARZ3NDEKTSV4RRQH65QH65QI", want: domain.ErrInvalidID},
		{name: "excluded letter L", in: "01ARZ3NDEKTSV4RRQH65QH65QL", want: domain.ErrInvalidID},
		{name: "excluded letter O", in: "01ARZ3NDEKTSV4RRQH65QH65QO", want: domain.ErrInvalidID},
		{name: "excluded letter U", in: "01ARZ3NDEKTSV4RRQH65QH65QU", want: domain.ErrInvalidID},
		{name: "punctuation", in: "01ARZ3NDEKTSV4RRQH65QH65Q!", want: domain.ErrInvalidID},
		{name: "overflowing first character", in: "81ARZ3NDEKTSV4RRQH65QH65QH", want: domain.ErrInvalidID},
		{name: "embedded space", in: "01ARZ3NDEKTSV RRQH65QH65QH", want: domain.ErrInvalidID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			input := tt.in

			// When
			id, err := domain.NewID(input)

			// Then
			assert.ErrorIs(t, err, tt.want)
			assert.Empty(t, id.String())
			assert.True(t, id.IsZero())
		})
	}
}

func Test_ID_String_ReturnsStoredValue_when_IDIsZero(t *testing.T) {
	// Given
	var id domain.ID

	// When
	rendered := id.String()

	// Then
	require.True(t, id.IsZero())
	require.Empty(t, rendered)
}

func Test_ID_Equal_ComparesByValue_when_BothAreSet(t *testing.T) {
	// Given
	a := mustID(t, ulidA)
	b := mustID(t, ulidA)
	c := mustID(t, ulidB)
	zero := domain.ID{}

	// When
	ab := a.Equal(b)
	ac := a.Equal(c)
	az := a.Equal(zero)
	zz := zero.Equal(domain.ID{})

	// Then
	require.True(t, ab)
	require.False(t, ac)
	require.False(t, az)
	require.True(t, zz)
}

func Test_TimestampMs_Time_ReturnsExactUTCInstant_when_MillisAreKnown(t *testing.T) {
	tests := []struct {
		name string
		ms   domain.TimestampMs
		want time.Time
	}{
		{
			name: "reference instant",
			ms:   refTime,
			want: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "unix epoch",
			ms:   domain.TimestampMs(0),
			want: time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "sub-second precision",
			ms:   domain.TimestampMs(1735689600123),
			want: time.Date(2025, time.January, 1, 0, 0, 0, 123_000_000, time.UTC),
		},
		{
			name: "before the epoch",
			ms:   domain.TimestampMs(-1000),
			want: time.Date(1969, time.December, 31, 23, 59, 59, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			ms := tt.ms

			// When
			got := ms.Time()

			// Then
			assert.Equal(t, tt.want, got)
			assert.True(t, got.Equal(tt.want))
			assert.Equal(t, time.UTC, got.Location())
			assert.Equal(t, int64(ms), got.UnixMilli())
		})
	}
}

func Test_TimestampMs_IsZero_Reports_when_ValueIsUnset(t *testing.T) {
	tests := []struct {
		name string
		ms   domain.TimestampMs
		want bool
	}{
		{name: "zero value", ms: 0, want: true},
		{name: "reference instant", ms: refTime, want: false},
		{name: "before epoch", ms: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			ms := tt.ms

			// When
			got := ms.IsZero()

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

// Compile-time proof that ULIDGenerator satisfies the injected generator contract.
var _ domain.IDGenerator = domain.NewULIDGenerator()

func Test_ULIDGenerator_New_MintsValidIdentifier_when_Called(t *testing.T) {
	// Given
	gen := domain.NewULIDGenerator()

	// When
	id := gen.New()

	// Then
	require.False(t, id.IsZero())
	require.Len(t, id.String(), 26)

	reparsed, err := domain.NewID(id.String())
	require.NoError(t, err)
	require.True(t, reparsed.Equal(id))
}

func Test_ULIDGenerator_NewAt_ProducesStrictlyAscendingIdentifiers_when_TimestampIsConstant(t *testing.T) {
	// Given
	const count = 512

	gen := domain.NewULIDGenerator()

	// When
	ids := make([]string, count)
	for i := range ids {
		ids[i] = gen.NewAt(refTime).String()
	}

	// Then
	require.Len(t, ids[0], 26)

	for i, id := range ids {
		require.Len(t, id, 26, "identifier %d must be 26 characters", i)
	}

	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	require.Equal(t, ids, sorted, "identifiers must be minted in lexicographic order")

	for i := 1; i < len(sorted); i++ {
		require.Less(t, sorted[i-1], sorted[i], "identifier %d must sort strictly after its predecessor", i)
	}

	// Every identifier in one millisecond shares the encoded timestamp prefix.
	require.Equal(t, ids[0][:10], ids[count-1][:10])
}

func Test_ULIDGenerator_NewAt_EncodesSuppliedTimestamp_when_InstantChanges(t *testing.T) {
	// Given
	gen := domain.NewULIDGenerator()

	// When
	first := gen.NewAt(refTime).String()
	later := gen.NewAt(refTime + 1).String()

	// Then
	require.Len(t, first, 26)
	require.Len(t, later, 26)
	require.NotEqual(t, first[:10], later[:10])
	require.Less(t, first, later)
}

func Test_ULIDGenerator_NewAt_ProducesUniqueIdentifiers_when_CalledConcurrently(t *testing.T) {
	// Given
	const (
		goroutines   = 8
		perGoroutine = 64
		total        = goroutines * perGoroutine
	)

	gen := domain.NewULIDGenerator()

	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, total)
		wg   sync.WaitGroup
	)

	// When
	for range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range perGoroutine {
				id := gen.NewAt(refTime).String()

				mu.Lock()
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Then
	require.Len(t, seen, total)
}
