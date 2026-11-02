// Package vault encrypts credentials at rest.
//
// Two rules shape it. First, a secret is sealed with AES-256-GCM so a tampered
// row fails to open rather than decrypting to garbage. Second, the plaintext has
// exactly two doors: Open for the executor, and nothing else. Everything that
// renders a credential for a human or a model goes through Redacted, which cannot
// return the secret by construction.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// Sentinel errors. ErrDecryptFailed deliberately carries no detail: distinguishing
// "wrong key" from "tampered ciphertext" from "wrong AAD" tells an attacker which
// of the three they got right.
var (
	ErrDecryptFailed  = errors.New("vault: cannot decrypt ciphertext")
	ErrInvalidKey     = errors.New("vault: invalid key material")
	ErrInvalidPayload = errors.New("vault: invalid payload")
)

// Key derivation parameters. Raising these invalidates existing ciphertext, so
// they are fixed and versioned rather than tuned per call.
const (
	// SaltBytes is the minimum salt length accepted. A salt shorter than the
	// hash output adds nothing, which is why it is a floor rather than a hint.
	SaltBytes = 16

	// KeyBytes is the AES-256 key length.
	KeyBytes = 32

	// MinIterations is the floor for PBKDF2-HMAC-SHA256. The current OWASP
	// guidance for this construction is 600_000; a caller may pass more.
	MinIterations = 600_000
)

// Params are the derivation inputs. They must be stored alongside the
// ciphertext, because a key derived with a different salt cannot open it.
type Params struct {
	Salt       []byte
	Iterations int
}

// Vault seals and opens secrets with one derived key.
type Vault struct {
	aead cipher.AEAD
	key  []byte
}

// New derives a key from a passphrase and the stored parameters.
//
// The caller owns the passphrase; it is never persisted here. Derivation is
// PBKDF2-HMAC-SHA256 from the standard library, chosen over a third-party KDF so
// the project keeps its pinned dependency set.
func New(passphrase []byte, params Params) (*Vault, error) {
	if len(passphrase) == 0 {
		return nil, fmt.Errorf("passphrase is empty: %w", ErrInvalidKey)
	}
	if len(params.Salt) < SaltBytes {
		return nil, fmt.Errorf("salt must be at least %d bytes, got %d: %w",
			SaltBytes, len(params.Salt), ErrInvalidKey)
	}

	iterations := params.Iterations
	if iterations == 0 {
		iterations = MinIterations
	}
	if iterations < MinIterations {
		return nil, fmt.Errorf("iterations %d is below the %d floor: %w",
			iterations, MinIterations, ErrInvalidKey)
	}

	key, err := pbkdf2.Key(sha256.New, string(passphrase), params.Salt, iterations, KeyBytes)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w: %w", ErrInvalidKey, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w: %w", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w: %w", ErrInvalidKey, err)
	}

	return &Vault{aead: aead, key: key}, nil
}

// NonceBytes reports the nonce length this vault produces.
func (v *Vault) NonceBytes() int {
	if v.aead == nil {
		return 0
	}
	return v.aead.NonceSize()
}

// Seal encrypts plaintext, binding aad as associated data.
//
// A fresh random nonce is generated on every call. Reusing a nonce under one key
// breaks GCM completely, so it is never derived from the plaintext or a counter.
func (v *Vault) Seal(plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	if v.aead == nil {
		return nil, nil, fmt.Errorf("vault has been zeroed: %w", ErrInvalidKey)
	}

	nonce = make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("read nonce: %w", err)
	}
	return nonce, v.aead.Seal(nil, nonce, plaintext, aad), nil
}

// Open decrypts ciphertext, requiring the same aad used to seal it.
//
// Every failure returns the same sentinel on purpose: a distinguishable error
// would let a caller probe which component was wrong.
func (v *Vault) Open(nonce, ciphertext, aad []byte) ([]byte, error) {
	if v.aead == nil {
		return nil, ErrDecryptFailed
	}
	if len(nonce) != v.aead.NonceSize() {
		return nil, fmt.Errorf("nonce is %d bytes, want %d: %w",
			len(nonce), v.aead.NonceSize(), ErrInvalidPayload)
	}

	plaintext, err := v.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// SealString seals a UTF-8 string.
func (v *Vault) SealString(plaintext string, aad []byte) (nonce, ciphertext []byte, err error) {
	return v.Seal([]byte(plaintext), aad)
}

// OpenString opens to a UTF-8 string.
func (v *Vault) OpenString(nonce, ciphertext, aad []byte) (string, error) {
	plaintext, err := v.Open(nonce, ciphertext, aad)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// Zero best-effort clears the derived key.
//
// The AEAD is dropped as well, not just the byte slice: cipher.AEAD keeps its own
// expanded key schedule, so clearing only the caller-visible copy would leave a
// working key alive and make Zero a false assurance.
func (v *Vault) Zero() {
	for i := range v.key {
		v.key[i] = 0
	}
	v.aead = nil
}

// NewSalt returns a fresh random salt.
func NewSalt() ([]byte, error) {
	salt := make([]byte, SaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("read salt: %w", err)
	}
	return salt, nil
}
