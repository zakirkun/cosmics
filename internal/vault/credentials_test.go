package vault_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/vault"
)

func openVaultTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "cosmics.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// testClock returns a fixed instant so createdAt is deterministic.
type testClock struct{}

func (testClock) Now() domain.TimestampMs { return 1_760_000_000_000 }

// newCredentialRepo builds a vault-backed repository over a fresh database.
func newCredentialRepo(t *testing.T) (*vault.CredentialRepo, *store.DB) {
	t.Helper()

	db := openVaultTestDB(t)
	v := newTestVault(t)

	repo, err := vault.NewCredentialRepo(db, v, testClock{})
	require.NoError(t, err)

	return repo, db
}

// newEngagementID returns the identifier of an engagement that exists in db.
//
// The credentials table has a foreign key to engagements, so a credential cannot
// be stored against a fabricated identifier. Tests therefore create the parent
// first, exactly as the application does.
func newEngagementID(t *testing.T, db *store.DB) domain.ID {
	t.Helper()

	target, err := domain.NewTarget(domain.TargetDomain, "portal.acme.test")
	require.NoError(t, err)

	engagement, err := domain.NewEngagement(domain.NewEngagementParams{
		Name:   "vault-test",
		Client: "Acme Corporation",
		Mode:   domain.ModeFull,
		Authorization: domain.Authorization{
			AuthorizedBy: "tester",
			AuthorizedAt: 1_759_999_999_000,
		},
		Targets: []domain.Target{target},
		Now:     1_760_000_000_000,
	})
	require.NoError(t, err)
	engagement.ID = domain.NewULIDGenerator().New()

	if err := store.NewEngagementRepo(db).Create(t.Context(), engagement, []domain.Target{target}); err != nil {
		require.NoError(t, err)
	}

	return engagement.ID
}

func Test_Store_persists_only_ciphertext_when_a_secret_is_saved(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	engagementID := newEngagementID(t, db)
	const secret = "sup3r-s3cret-p@ssw0rd"

	// When
	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: engagementID,
		Kind:         store.CredentialPassword,
		Username:     "operator",
	}, secret)

	// Then
	require.NoError(t, err)

	row, err := store.NewCredentialRowRepo(db).Get(ctx, id)
	require.NoError(t, err)

	assert.NotEmpty(t, row.Ciphertext)
	assert.NotEmpty(t, row.Nonce)
	assert.Equal(t, store.KDFArgon2id, row.KDF)

	assert.NotContains(t, string(row.Ciphertext), secret,
		"the ciphertext must not contain the plaintext secret")
	assert.NotContains(t, row.Hint, secret,
		"the hint is a note, never the secret")
}

func Test_Resolve_returns_the_plaintext_when_the_id_exists(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	const secret = "correct-horse-battery-staple"

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialPassword,
	}, secret)
	require.NoError(t, err)

	// When
	got, err := repo.Resolve(ctx, id)

	// Then
	require.NoError(t, err)
	assert.Equal(t, secret, got)
}

func Test_Redacted_never_returns_the_plaintext_when_called(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	const secret = "do-not-print-me"

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialToken,
	}, secret)
	require.NoError(t, err)

	// When
	got, err := repo.Redacted(ctx, id)

	// Then
	require.NoError(t, err)
	assert.Equal(t, vault.RedactedPrefix+id.String(), got)
	assert.NotContains(t, got, secret)
}

func Test_Redacted_output_does_not_contain_the_secret_substring(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	const secret = "averylongsecretvaluethatmustnotappear"

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialToken,
	}, secret)
	require.NoError(t, err)

	// When
	redacted, err := repo.Redacted(ctx, id)

	// Then
	require.NoError(t, err)
	for i := 0; i+8 <= len(secret); i++ {
		fragment := secret[i : i+8]
		assert.NotContains(t, redacted, fragment,
			"no eight-character window of the secret may appear in a redacted value")
	}
}

func Test_Credential_Redacted_includes_the_identifier_and_not_the_ciphertext(t *testing.T) {
	// Given
	cred := vault.Credential{
		ID:         domain.NewULIDGenerator().New(),
		Ciphertext: []byte{0x01, 0x02, 0x03},
	}

	// When
	got := cred.Redacted()

	// Then
	assert.Contains(t, got, cred.ID.String())
	assert.NotContains(t, got, "\x01")
}

func Test_Store_rejects_an_unknown_kind_when_given_one(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)

	// When
	_, err := repo.Store(context.Background(), vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialKind("telepathy"),
	}, "secret")

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidCredential)
	assert.Contains(t, err.Error(), "kind")
}

func Test_Store_rejects_an_empty_secret_when_given_one(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)

	// When
	_, err := repo.Store(context.Background(), vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialPassword,
	}, "")

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidCredential)
}

func Test_Store_rejects_a_missing_engagement_when_zero(t *testing.T) {
	// Given
	repo, _ := newCredentialRepo(t)

	// When
	_, err := repo.Store(context.Background(), vault.Credential{
		Kind: store.CredentialPassword,
	}, "secret")

	// Then
	require.ErrorIs(t, err, vault.ErrInvalidCredential)
	assert.Contains(t, err.Error(), "engagement")
}

func Test_Resolve_returns_ErrCredentialMissing_when_the_id_does_not_exist(t *testing.T) {
	// Given
	repo, _ := newCredentialRepo(t)

	// When
	_, err := repo.Resolve(context.Background(), domain.NewULIDGenerator().New())

	// Then
	require.ErrorIs(t, err, vault.ErrCredentialMissing)
}

func Test_Resolve_fails_when_the_vault_key_is_wrong(t *testing.T) {
	// Given: a credential sealed under one vault, then read by another.
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	const secret = "cross-vault-secret" //nolint:gosec // G101: a fixture value, not a real credential

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialPassword,
	}, secret)
	require.NoError(t, err)

	otherVault, err := vault.New([]byte("a different passphrase"), vault.Params{
		Salt:       mustSalt(t),
		Iterations: vault.MinIterations,
	})
	require.NoError(t, err)
	defer otherVault.Zero()

	otherRepo, err := vault.NewCredentialRepo(db, otherVault, testClock{})
	require.NoError(t, err)

	// When
	_, err = otherRepo.Resolve(ctx, id)

	// Then
	require.ErrorIs(t, err, vault.ErrDecryptFailed,
		"a ciphertext sealed under one key must not open under another")
}

func Test_ListForEngagement_returns_only_redacted_material_when_called(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()
	engagementID := newEngagementID(t, db)
	const secretOne = "first-secret-value"
	const secretTwo = "second-secret-value"

	_, err := repo.Store(ctx, vault.Credential{EngagementID: engagementID, Kind: store.CredentialPassword}, secretOne)
	require.NoError(t, err)
	_, err = repo.Store(ctx, vault.Credential{EngagementID: engagementID, Kind: store.CredentialToken}, secretTwo)
	require.NoError(t, err)

	// When
	creds, err := repo.ListForEngagement(ctx, engagementID)

	// Then
	require.NoError(t, err)
	require.Len(t, creds, 2)

	for _, cred := range creds {
		assert.NotContains(t, string(cred.Ciphertext), secretOne)
		assert.NotContains(t, string(cred.Ciphertext), secretTwo)
		assert.NotContains(t, cred.Redacted(), secretOne)
		assert.NotContains(t, cred.Redacted(), secretTwo)
	}
}

func Test_MarkVerified_updates_the_row_when_called(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialPassword,
	}, "secret")
	require.NoError(t, err)

	// When
	err = repo.MarkVerified(ctx, id, true)

	// Then
	require.NoError(t, err)

	row, err := store.NewCredentialRowRepo(db).Get(ctx, id)
	require.NoError(t, err)
	assert.True(t, row.Verified)
}

func Test_Delete_removes_the_row_when_called(t *testing.T) {
	// Given
	repo, db := newCredentialRepo(t)
	ctx := context.Background()

	id, err := repo.Store(ctx, vault.Credential{
		EngagementID: newEngagementID(t, db),
		Kind:         store.CredentialCookie,
	}, "secret")
	require.NoError(t, err)

	// When
	err = repo.Delete(ctx, id)

	// Then
	require.NoError(t, err)

	_, err = store.NewCredentialRowRepo(db).Get(ctx, id)
	require.ErrorIs(t, err, store.ErrCredentialNotFound)
}

// mustSalt returns a fresh salt or fails the test.
func mustSalt(t *testing.T) []byte {
	t.Helper()

	salt, err := vault.NewSalt()
	require.NoError(t, err)
	return salt
}
