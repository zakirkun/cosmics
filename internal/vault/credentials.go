package vault

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// Sentinel errors for credential handling.
var (
	ErrInvalidCredential = errors.New("vault: invalid credential")
	ErrCredentialMissing = errors.New("vault: credential not found")
)

// RedactedPrefix is what a caller sees in place of a secret.
const RedactedPrefix = "vault:"

// Credential is one stored secret in the shape the application uses.
//
// Nonce and Ciphertext are the only representation of the secret: there is
// deliberately no Plaintext field, so a Credential cannot leak a secret by being
// formatted, logged, or marshalled into a report.
type Credential struct {
	ID           domain.ID
	EngagementID domain.ID
	AssetID      domain.ID
	Username     string
	Hint         string
	Kind         store.CredentialKind
	KDF          store.CredentialKDF
	Nonce        []byte
	Ciphertext   []byte
	Verified     bool
	ValidUntil   domain.TimestampMs
	CreatedAt    domain.TimestampMs
}

// Redacted renders the credential for a report or a log.
//
// This is the obvious thing to do with a Credential, which is the point: the
// safe call is also the convenient one.
func (c Credential) Redacted() string {
	return RedactedPrefix + c.ID.String()
}

// Validate checks the fields the caller controls before sealing.
func (c Credential) Validate() error {
	if c.EngagementID.IsZero() {
		return fmt.Errorf("engagement id: %w", ErrInvalidCredential)
	}
	if !c.Kind.Valid() {
		return fmt.Errorf("kind %q: %w", c.Kind, ErrInvalidCredential)
	}
	return nil
}

// aadFor binds a ciphertext to the row it belongs to.
//
// The engagement id is in the associated data, so a ciphertext copied from one
// engagement's row into another's fails to open rather than yielding a secret.
func aadFor(engagementID domain.ID, kind store.CredentialKind) []byte {
	return []byte("credential|" + engagementID.String() + "|" + string(kind))
}

// CredentialRepo stores sealed secrets.
type CredentialRepo struct {
	rows  *store.CredentialRowRepo
	vault *Vault
	clock domain.Clock
}

// NewCredentialRepo returns a repository.
func NewCredentialRepo(db *store.DB, vault *Vault, clock domain.Clock) (*CredentialRepo, error) {
	if db == nil {
		return nil, fmt.Errorf("db: %w", ErrInvalidCredential)
	}
	if vault == nil {
		return nil, fmt.Errorf("vault: %w", ErrInvalidCredential)
	}
	if clock == nil {
		return nil, fmt.Errorf("clock: %w", ErrInvalidCredential)
	}

	return &CredentialRepo{
		rows:  store.NewCredentialRowRepo(db),
		vault: vault,
		clock: clock,
	}, nil
}

// Store seals a plaintext secret and persists only the ciphertext.
//
// The plaintext argument is the single point where a secret enters the system.
// It is not stored, not logged, and not returned.
func (r *CredentialRepo) Store(ctx context.Context, cred Credential, plaintextSecret string) (domain.ID, error) {
	if err := cred.Validate(); err != nil {
		return domain.ID{}, err
	}
	if plaintextSecret == "" {
		return domain.ID{}, fmt.Errorf("plaintext secret is empty: %w", ErrInvalidCredential)
	}

	aad := aadFor(cred.EngagementID, cred.Kind)

	nonce, ciphertext, err := r.vault.SealString(plaintextSecret, aad)
	if err != nil {
		return domain.ID{}, fmt.Errorf("seal secret: %w", err)
	}

	id := domain.NewULIDGenerator().New()

	row := store.CredentialRow{
		ID:           id,
		EngagementID: cred.EngagementID,
		AssetID:      cred.AssetID,
		Username:     cred.Username,
		Ciphertext:   ciphertext,
		Nonce:        nonce,
		KDF:          store.KDFArgon2id,
		Hint:         cred.Hint,
		Kind:         cred.Kind,
		ValidUntil:   cred.ValidUntil,
		Verified:     cred.Verified,
		CreatedAt:    r.clock.Now(),
	}

	if err := r.rows.Insert(ctx, row); err != nil {
		return domain.ID{}, fmt.Errorf("insert credential row: %w", err)
	}
	return id, nil
}

// Resolve returns the plaintext secret for executor use.
//
// The name is a warning: the returned value goes straight into a container
// environment or a tool argument, never into a log line, a report, or an LLM
// message.
func (r *CredentialRepo) Resolve(ctx context.Context, id domain.ID) (string, error) {
	row, err := r.rows.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read credential %s: %w: %w", id, ErrCredentialMissing, err)
	}

	plaintext, err := r.vault.OpenString(row.Nonce, row.Ciphertext, aadFor(row.EngagementID, row.Kind))
	if err != nil {
		return "", fmt.Errorf("open credential %s: %w", id, err)
	}
	return plaintext, nil
}

// Redacted returns the reference form of a credential for reports and logs.
func (r *CredentialRepo) Redacted(ctx context.Context, id domain.ID) (string, error) {
	row, err := r.rows.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read credential %s: %w: %w", id, ErrCredentialMissing, err)
	}
	return RedactedPrefix + row.ID.String(), nil
}

// ListForEngagement returns every credential of an engagement, redacted form
// only. There is intentionally no bulk variant that returns plaintext.
func (r *CredentialRepo) ListForEngagement(ctx context.Context, engagementID domain.ID) ([]Credential, error) {
	rows, err := r.rows.ListByEngagement(ctx, engagementID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}

	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		out = append(out, Credential{
			ID:           row.ID,
			EngagementID: row.EngagementID,
			AssetID:      row.AssetID,
			Username:     row.Username,
			Hint:         row.Hint,
			Kind:         row.Kind,
			KDF:          row.KDF,
			Nonce:        row.Nonce,
			Ciphertext:   row.Ciphertext,
			Verified:     row.Verified,
			ValidUntil:   row.ValidUntil,
			CreatedAt:    row.CreatedAt,
		})
	}
	return out, nil
}

// MarkVerified records that a credential was proven valid.
func (r *CredentialRepo) MarkVerified(ctx context.Context, id domain.ID, verified bool) error {
	if err := r.rows.MarkVerified(ctx, id, verified); err != nil {
		return fmt.Errorf("mark credential %s: %w", id, err)
	}
	return nil
}

// Delete removes a credential row.
func (r *CredentialRepo) Delete(ctx context.Context, id domain.ID) error {
	if err := r.rows.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete credential %s: %w", id, err)
	}
	return nil
}
