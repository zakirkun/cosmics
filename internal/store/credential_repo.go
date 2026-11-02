package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors for credential persistence.
var (
	ErrInvalidCredentialRow = errors.New("store: invalid credential row")
	ErrCredentialNotFound   = errors.New("store: credential not found")
)

// CredentialKDF names the key-derivation scheme that produced a ciphertext.
//
// The value is persisted so a future change of derivation can still open old
// rows: without it, a KDF migration would make every existing secret unreadable.
type CredentialKDF string

// Supported derivation schemes, matching the credentials.secret_kdf CHECK list.
const (
	KDFArgon2id  CredentialKDF = "argon2id"
	KDFOSKeyring CredentialKDF = "os_keyring"
)

// CredentialKind classifies a stored secret, matching the credentials.kind CHECK list.
type CredentialKind string

// Credential kinds.
const (
	CredentialPassword CredentialKind = "password"
	CredentialNTLM     CredentialKind = "ntlm"
	CredentialSSHKey   CredentialKind = "ssh_key"
	CredentialToken    CredentialKind = "token"
	CredentialCookie   CredentialKind = "cookie"
	CredentialCert     CredentialKind = "cert"
)

// AllCredentialKinds lists every accepted kind.
var AllCredentialKinds = []CredentialKind{
	CredentialPassword, CredentialNTLM, CredentialSSHKey,
	CredentialToken, CredentialCookie, CredentialCert,
}

// Valid reports whether the kind is accepted by the schema.
func (k CredentialKind) Valid() bool {
	for _, declared := range AllCredentialKinds {
		if declared == k {
			return true
		}
	}
	return false
}

// Valid reports whether the derivation scheme is accepted by the schema.
func (k CredentialKDF) Valid() bool {
	return k == KDFArgon2id || k == KDFOSKeyring
}

// CredentialRow is one credentials row.
//
// Ciphertext and Nonce are the only representation of the secret here; there is
// no plaintext field, so a row cannot leak a secret by being formatted.
type CredentialRow struct {
	ID           domain.ID
	EngagementID domain.ID
	AssetID      domain.ID
	Username     string

	Ciphertext []byte
	Nonce      []byte
	KDF        CredentialKDF
	Hint       string

	Kind CredentialKind

	SourceToolRunID domain.ID
	ValidUntil      domain.TimestampMs
	Verified        bool
	CreatedAt       domain.TimestampMs
}

// Validate enforces the schema's NOT NULL and CHECK constraints before a write.
func (r CredentialRow) Validate() error {
	if r.EngagementID.IsZero() {
		return fmt.Errorf("engagement_id: %w", ErrInvalidCredentialRow)
	}
	if len(r.Ciphertext) == 0 || len(r.Nonce) == 0 {
		return fmt.Errorf("ciphertext and nonce must both be present: %w", ErrInvalidCredentialRow)
	}
	if !r.KDF.Valid() {
		return fmt.Errorf("secret_kdf %q: %w", r.KDF, ErrInvalidCredentialRow)
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("kind %q: %w", r.Kind, ErrInvalidCredentialRow)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("created_at: %w", ErrInvalidCredentialRow)
	}
	return nil
}

// CredentialRowRepo persists credentials.
type CredentialRowRepo struct {
	db *DB
}

// NewCredentialRowRepo returns a repository over the credentials table.
func NewCredentialRowRepo(db *DB) *CredentialRowRepo {
	return &CredentialRowRepo{db: db}
}

// Insert writes one credential row.
func (r *CredentialRowRepo) Insert(ctx context.Context, row CredentialRow) error {
	if err := row.Validate(); err != nil {
		return err
	}

	const query = `
		INSERT INTO credentials (
			id, engagement_id, asset_id, username,
			secret_ciphertext, secret_nonce, secret_kdf, secret_hint,
			kind, source_tool_run_id, valid_until, verified, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := r.db.Writer().ExecContext(ctx, query,
		row.ID.String(),
		row.EngagementID.String(),
		nullableID(row.AssetID),
		nullableString(row.Username),
		row.Ciphertext,
		row.Nonce,
		string(row.KDF),
		nullableString(row.Hint),
		string(row.Kind),
		nullableID(row.SourceToolRunID),
		nullableTimestamp(row.ValidUntil),
		boolToInt(row.Verified),
		int64(row.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert credential: %w", err)
	}
	return nil
}

// Get reads one credential row by identifier.
func (r *CredentialRowRepo) Get(ctx context.Context, id domain.ID) (CredentialRow, error) {
	const query = `
		SELECT id, engagement_id, asset_id, username,
		       secret_ciphertext, secret_nonce, secret_kdf, secret_hint,
		       kind, source_tool_run_id, valid_until, verified, created_at
		FROM credentials WHERE id = ?`

	var (
		row           CredentialRow
		idText        string
		engagementTxt string
		assetID       sql.NullString
		username      sql.NullString
		kdf           string
		hint          sql.NullString
		kind          string
		sourceRun     sql.NullString
		validUntil    sql.NullInt64
		verified      int
		createdAt     int64
	)

	err := r.db.Reader().QueryRowContext(ctx, query, id.String()).Scan(
		&idText, &engagementTxt, &assetID, &username,
		&row.Ciphertext, &row.Nonce, &kdf, &hint,
		&kind, &sourceRun, &validUntil, &verified, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialRow{}, fmt.Errorf("credential %s: %w", id, ErrCredentialNotFound)
	}
	if err != nil {
		return CredentialRow{}, fmt.Errorf("read credential %s: %w", id, err)
	}

	row, err = credentialRowFromColumns(
		idText, engagementTxt, assetID, username,
		row.Ciphertext, row.Nonce, kdf, hint,
		kind, sourceRun, validUntil, verified, createdAt,
	)
	if err != nil {
		return CredentialRow{}, err
	}

	return row, nil
}

// credentialRowFromColumns assembles a row from raw column values.
//
// Identifiers are parsed through domain.NewID rather than cast: domain.ID wraps a
// validated raw string, so a blind conversion would accept an empty value that the
// domain layer considers zero-but-present.
func credentialRowFromColumns(
	idText, engagementTxt string,
	assetID, username sql.NullString,
	ciphertext, nonce []byte,
	kdf string, hint sql.NullString,
	kind string, sourceRun sql.NullString,
	validUntil sql.NullInt64, verified int, createdAt int64,
) (CredentialRow, error) {
	id, err := domain.NewID(idText)
	if err != nil {
		return CredentialRow{}, fmt.Errorf("credential id %q: %w", idText, err)
	}
	engagementID, err := domain.NewID(engagementTxt)
	if err != nil {
		return CredentialRow{}, fmt.Errorf("credential %s engagement id %q: %w", idText, engagementTxt, err)
	}

	row := CredentialRow{
		ID:           id,
		EngagementID: engagementID,
		Ciphertext:   ciphertext,
		Nonce:        nonce,
		KDF:          CredentialKDF(kdf),
		Username:     username.String,
		Hint:         hint.String,
		Kind:         CredentialKind(kind),
		ValidUntil:   domain.TimestampMs(validUntil.Int64),
		Verified:     verified == 1,
		CreatedAt:    domain.TimestampMs(createdAt),
	}

	if assetID.Valid && assetID.String != "" {
		asset, err := domain.NewID(assetID.String)
		if err != nil {
			return CredentialRow{}, fmt.Errorf("credential %s asset id %q: %w", idText, assetID.String, err)
		}
		row.AssetID = asset
	}
	if sourceRun.Valid && sourceRun.String != "" {
		run, err := domain.NewID(sourceRun.String)
		if err != nil {
			return CredentialRow{}, fmt.Errorf("credential %s source run id %q: %w", idText, sourceRun.String, err)
		}
		row.SourceToolRunID = run
	}

	return row, nil
}

// ListByEngagement returns every credential of an engagement, newest first.
//
// Ordering is by created_at then id so the output is stable even when two rows
// share a millisecond.
func (r *CredentialRowRepo) ListByEngagement(ctx context.Context, engagementID domain.ID) ([]CredentialRow, error) {
	const query = `
		SELECT id, engagement_id, asset_id, username,
		       secret_ciphertext, secret_nonce, secret_kdf, secret_hint,
		       kind, source_tool_run_id, valid_until, verified, created_at
		FROM credentials
		WHERE engagement_id = ?
		ORDER BY created_at DESC, id ASC`

	rows, err := r.db.Reader().QueryContext(ctx, query, engagementID.String())
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CredentialRow
	for rows.Next() {
		var (
			row           CredentialRow
			idText        string
			engagementTxt string
			assetID       sql.NullString
			username      sql.NullString
			kdf           string
			hint          sql.NullString
			kind          string
			sourceRun     sql.NullString
			validUntil    sql.NullInt64
			verified      int
			createdAt     int64
		)

		if err := rows.Scan(
			&idText, &engagementTxt, &assetID, &username,
			&row.Ciphertext, &row.Nonce, &kdf, &hint,
			&kind, &sourceRun, &validUntil, &verified, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}

		assembled, err := credentialRowFromColumns(
			idText, engagementTxt, assetID, username,
			row.Ciphertext, row.Nonce, kdf, hint,
			kind, sourceRun, validUntil, verified, createdAt,
		)
		if err != nil {
			return nil, err
		}

		out = append(out, assembled)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	return out, nil
}

// MarkVerified records that a credential was proven valid.
func (r *CredentialRowRepo) MarkVerified(ctx context.Context, id domain.ID, verified bool) error {
	const query = `UPDATE credentials SET verified = ? WHERE id = ?`

	result, err := r.db.Writer().ExecContext(ctx, query, boolToInt(verified), id.String())
	if err != nil {
		return fmt.Errorf("mark credential %s verified: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("credential %s: %w", id, ErrCredentialNotFound)
	}
	return nil
}

// Delete removes one credential row.
func (r *CredentialRowRepo) Delete(ctx context.Context, id domain.ID) error {
	const query = `DELETE FROM credentials WHERE id = ?`

	result, err := r.db.Writer().ExecContext(ctx, query, id.String())
	if err != nil {
		return fmt.Errorf("delete credential %s: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("credential %s: %w", id, ErrCredentialNotFound)
	}
	return nil
}

// nullableID converts a zero identifier to SQL NULL.
func nullableID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

// nullableString converts an empty string to SQL NULL.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullableTimestamp converts a zero timestamp to SQL NULL.
func nullableTimestamp(ts domain.TimestampMs) any {
	if ts.IsZero() {
		return nil
	}
	return int64(ts)
}
