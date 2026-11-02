package vault_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/vault"
)

// testParams returns derivation parameters at the floor, so tests stay fast
// without weakening the production floor.
func testParams(t *testing.T) vault.Params {
	t.Helper()

	salt, err := vault.NewSalt()
	require.NoError(t, err)

	return vault.Params{Salt: salt, Iterations: vault.MinIterations}
}

// newTestVault builds a vault with a throwaway passphrase.
func newTestVault(t *testing.T) *vault.Vault {
	t.Helper()

	v, err := vault.New([]byte("a-test-passphrase"), testParams(t))
	require.NoError(t, err)
	t.Cleanup(v.Zero)
	return v
}

func Test_Seal_Open_roundtrips_when_the_same_aad_is_used(t *testing.T) {
	// Given
	v := newTestVault(t)
	secret := []byte("hunter2-correct-horse")
	aad := []byte("credential:eng-1")

	// When
	nonce, ciphertext, err := v.Seal(secret, aad)
	require.NoError(t, err)

	// Then
	got, err := v.Open(nonce, ciphertext, aad)
	require.NoError(t, err)
	assert.Equal(t, secret, got)
}

func Test_Open_fails_when_the_aad_differs(t *testing.T) {
	// Given
	v := newTestVault(t)
	nonce, ciphertext, err := v.Seal([]byte("secret"), []byte("credential:eng-1"))
	require.NoError(t, err)

	// When
	_, err = v.Open(nonce, ciphertext, []byte("credential:eng-2"))

	// Then
	require.ErrorIs(t, err, vault.ErrDecryptFailed)
}

func Test_Open_fails_when_the_ciphertext_is_tampered_with(t *testing.T) {
	// Given
	v := newTestVault(t)
	aad := []byte("aad")
	nonce, ciphertext, err := v.Seal([]byte("secret"), aad)
	require.NoError(t, err)

	tampered := bytes.Clone(ciphertext)
	tampered[0] ^= 0xFF

	// When
	_, err = v.Open(nonce, tampered, aad)

	// Then
	require.ErrorIs(t, err, vault.ErrDecryptFailed)
}

func Test_Open_fails_when_the_nonce_is_the_wrong_length(t *testing.T) {
	// Given
	v := newTestVault(t)
	_, ciphertext, err := v.Seal([]byte("secret"), nil)
	require.NoError(t, err)

	// When
	_, err = v.Open([]byte{1, 2, 3}, ciphertext, nil)

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidPayload)
}

func Test_Seal_produces_a_different_nonce_when_called_twice_on_identical_input(t *testing.T) {
	// Given
	v := newTestVault(t)
	secret := []byte("identical plaintext")
	aad := []byte("identical aad")

	// When
	nonceOne, ciphertextOne, err := v.Seal(secret, aad)
	require.NoError(t, err)
	nonceTwo, ciphertextTwo, err := v.Seal(secret, aad)
	require.NoError(t, err)

	// Then
	assert.NotEqual(t, nonceOne, nonceTwo,
		"nonce reuse under one key breaks GCM completely; every Seal must draw fresh randomness")
	assert.NotEqual(t, ciphertextOne, ciphertextTwo,
		"identical plaintext must not produce identical ciphertext")
}

func Test_New_derives_the_same_key_when_given_the_same_passphrase_and_salt(t *testing.T) {
	// Given
	params := testParams(t)
	passphrase := []byte("same passphrase")

	first, err := vault.New(passphrase, params)
	require.NoError(t, err)
	defer first.Zero()

	second, err := vault.New(passphrase, params)
	require.NoError(t, err)
	defer second.Zero()

	aad := []byte("aad")
	nonce, ciphertext, err := first.Seal([]byte("payload"), aad)
	require.NoError(t, err)

	// When
	got, err := second.Open(nonce, ciphertext, aad)

	// Then
	require.NoError(t, err, "the same inputs must derive a key that can open the payload")
	assert.Equal(t, []byte("payload"), got)
}

func Test_New_derives_a_different_key_when_the_salt_differs(t *testing.T) {
	// Given
	passphrase := []byte("same passphrase")

	first, err := vault.New(passphrase, testParams(t))
	require.NoError(t, err)
	defer first.Zero()

	second, err := vault.New(passphrase, testParams(t))
	require.NoError(t, err)
	defer second.Zero()

	aad := []byte("aad")
	nonce, ciphertext, err := first.Seal([]byte("payload"), aad)
	require.NoError(t, err)

	// When
	_, err = second.Open(nonce, ciphertext, aad)

	// Then
	require.ErrorIs(t, err, vault.ErrDecryptFailed,
		"a different salt must derive a different key")
}

func Test_New_rejects_an_empty_passphrase_when_given_one(t *testing.T) {
	// Given
	params := testParams(t)

	// When
	_, err := vault.New(nil, params)

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidKey)
}

func Test_New_rejects_a_short_salt_when_given_one(t *testing.T) {
	// Given
	params := vault.Params{Salt: []byte{1, 2, 3}, Iterations: vault.MinIterations}

	// When
	_, err := vault.New([]byte("passphrase"), params)

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidKey)
	assert.Contains(t, err.Error(), "salt")
}

func Test_New_rejects_iterations_below_the_floor_when_given_fewer(t *testing.T) {
	// Given
	salt, err := vault.NewSalt()
	require.NoError(t, err)
	params := vault.Params{Salt: salt, Iterations: 1}

	// When
	_, err = vault.New([]byte("passphrase"), params)

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidKey)
	assert.Contains(t, err.Error(), "iterations")
}

func Test_NewSalt_returns_a_distinct_value_when_called_twice(t *testing.T) {
	// Given / When
	first, err := vault.NewSalt()
	require.NoError(t, err)
	second, err := vault.NewSalt()
	require.NoError(t, err)

	// Then
	assert.Len(t, first, vault.SaltBytes)
	assert.NotEqual(t, first, second)
}

func Test_SealString_OpenString_roundtrips_when_used_together(t *testing.T) {
	// Given
	v := newTestVault(t)
	aad := []byte("credential:eng-7")

	// When
	nonce, ciphertext, err := v.SealString("p@ssw0rd", aad)
	require.NoError(t, err)
	got, err := v.OpenString(nonce, ciphertext, aad)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "p@ssw0rd", got)
}

func Test_Zero_clears_the_key_when_called(t *testing.T) {
	// Given
	v := newTestVault(t)
	aad := []byte("aad")
	nonce, ciphertext, err := v.Seal([]byte("payload"), aad)
	require.NoError(t, err)

	// When
	v.Zero()

	// Then
	_, err = v.Open(nonce, ciphertext, aad)
	require.ErrorIs(t, err, vault.ErrDecryptFailed,
		"a zeroed key must not open anything")
}
