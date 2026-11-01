package ids_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/ids"
)

func Test_WSTG_loads_the_embedded_reference_set_when_requested(t *testing.T) {
	// Given / When
	set, err := ids.WSTG()

	// Then
	require.NoError(t, err)
	require.NotNil(t, set)
	assert.Equal(t, "wstg", set.Name())
	assert.Positive(t, set.Len())
}

func Test_ATTACK_loads_the_embedded_reference_set_when_requested(t *testing.T) {
	// Given / When
	set, err := ids.ATTACK()

	// Then
	require.NoError(t, err)
	require.NotNil(t, set)
	assert.Equal(t, "attack", set.Name())
	assert.Positive(t, set.Len())
}

func Test_WSTG_Has_accepts_a_known_identifier(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	got := set.Has("WSTG-INPV-05")

	// Then
	require.True(t, got)
}

func Test_WSTG_Has_rejects_the_misspelled_AUTHN_family_when_checked(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	got := set.Has("WSTG-AUTHN-01")

	// Then
	require.False(t, got, "WSTG uses ATHN, not AUTHN; a typo must not resolve")
}

func Test_WSTG_Lookup_returns_the_title_when_the_identifier_exists(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	ref, err := set.Lookup("WSTG-INPV-05")

	// Then
	require.NoError(t, err)
	assert.Equal(t, "WSTG-INPV-05", ref.ID)
	assert.Contains(t, ref.Title, "SQL Injection")
}

func Test_WSTG_Lookup_returns_errUnknownID_when_the_identifier_is_absent(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	_, err = set.Lookup("WSTG-NOPE-99")

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ids.ErrUnknownID)
}

func Test_WSTG_Normalize_returns_an_empty_string_for_empty_input(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	got, err := set.Normalize("")

	// Then
	require.NoError(t, err)
	assert.Empty(t, got)
}

func Test_WSTG_Normalize_returns_the_identifier_when_it_is_known(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	got, err := set.Normalize("WSTG-ATHN-02")

	// Then
	require.NoError(t, err)
	assert.Equal(t, "WSTG-ATHN-02", got)
}

func Test_WSTG_Normalize_returns_errUnknownID_when_the_identifier_is_invalid(t *testing.T) {
	// Given
	set, err := ids.WSTG()
	require.NoError(t, err)

	// When
	_, err = set.Normalize("WSTG-BOGUS-01")

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ids.ErrUnknownID)
}

func Test_ATTACK_Has_accepts_a_technique_and_a_subtechnique(t *testing.T) {
	// Given
	set, err := ids.ATTACK()
	require.NoError(t, err)

	tests := []struct {
		name string
		id   string
	}{
		{"base technique", "T1190"},
		{"sub-technique", "T1595.002"},
		{"credential dumping", "T1003"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := set.Has(tt.id)

			// Then
			assert.True(t, got, "%s should be present", tt.id)
		})
	}
}

func Test_ATTACK_Has_rejects_identifiers_that_do_not_match_the_scheme(t *testing.T) {
	// Given
	set, err := ids.ATTACK()
	require.NoError(t, err)

	tests := []struct {
		name string
		id   string
	}{
		{"missing T prefix", "1190"},
		{"too few digits", "T119"},
		{"trailing whitespace", "T1190 "},
		{"unknown technique", "T9999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := set.Has(tt.id)

			// Then
			assert.False(t, got, "%q should not resolve", tt.id)
		})
	}
}

func Test_ATTACK_Len_counts_every_technique_and_subtechnique(t *testing.T) {
	// Given
	set, err := ids.ATTACK()
	require.NoError(t, err)

	// When
	got := set.Len()

	// Then
	assert.Greater(t, got, 100, "the embedded ATT&CK set should cover well over 100 techniques")
}
