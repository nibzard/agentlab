package integrations

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/agentlab/agentlab/internal/db"
)

// Store manages integration persistence with encrypted secrets at rest.
//
// Secrets are encrypted using AES-GCM with a daemon-managed encryption key.
// They are only decrypted in the proxy hot path, never written to disk in plaintext.
type Store struct {
	store  *db.Store
	encKey []byte // AES-256 encryption key
}

// NewStore creates a new integration store.
// The encKey must be exactly 32 bytes (AES-256).
func NewStore(store *db.Store, encKey []byte) (*Store, error) {
	if len(encKey) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(encKey))
	}
	return &Store{
		store:  store,
		encKey: encKey,
	}, nil
}

// GenerateEncryptionKey generates a new random 32-byte AES-256 key.
func GenerateEncryptionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	return key, nil
}

// EncryptionKeyHex returns the hex-encoded encryption key for storage in config.
func EncryptionKeyHex(key []byte) string {
	return hex.EncodeToString(key)
}

// ParseEncryptionKeyHex decodes a hex-encoded encryption key.
func ParseEncryptionKeyHex(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// Create inserts a new integration with its secret encrypted at rest.
func (s *Store) Create(ctx context.Context, integ *Integration) error {
	if err := integ.Validate(); err != nil {
		return err
	}
	encryptedSecret, err := s.encrypt(integ.Secret)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEncryptFailed, err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.store.DB.ExecContext(ctx,
		`INSERT INTO integrations (name, type, target, encrypted_secret, secret_type, secret_header, username, provider, attach_mode, attach_selector, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		integ.Name, string(integ.Type), integ.Target, encryptedSecret,
		integ.SecretType, integ.SecretHeader, integ.Username, integ.Provider,
		string(integ.AttachMode), integ.AttachSelector,
		now, now,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateName
		}
		return fmt.Errorf("insert integration: %w", err)
	}
	id, _ := result.LastInsertId()
	integ.ID = id
	integ.CreatedAt = time.Now().UTC()
	integ.UpdatedAt = integ.CreatedAt
	return nil
}

// Get retrieves an integration by name, decrypting its secret.
func (s *Store) Get(ctx context.Context, name string) (*Integration, error) {
	row := s.store.DB.QueryRowContext(ctx,
		`SELECT id, name, type, target, encrypted_secret, secret_type, secret_header, username, provider, attach_mode, attach_selector, created_at, updated_at
		 FROM integrations WHERE name = ?`, name)
	return s.scanIntegration(row)
}

// GetByID retrieves an integration by ID, decrypting its secret.
func (s *Store) GetByID(ctx context.Context, id int64) (*Integration, error) {
	row := s.store.DB.QueryRowContext(ctx,
		`SELECT id, name, type, target, encrypted_secret, secret_type, secret_header, username, provider, attach_mode, attach_selector, created_at, updated_at
		 FROM integrations WHERE id = ?`, id)
	return s.scanIntegration(row)
}

// List returns all integrations, decrypting their secrets.
func (s *Store) List(ctx context.Context) ([]*Integration, error) {
	rows, err := s.store.DB.QueryContext(ctx,
		`SELECT id, name, type, target, encrypted_secret, secret_type, secret_header, username, provider, attach_mode, attach_selector, created_at, updated_at
		 FROM integrations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	defer rows.Close()
	var result []*Integration
	for rows.Next() {
		integ, err := s.scanIntegrationFromRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, integ)
	}
	return result, rows.Err()
}

// ListForSandbox returns integrations that match the given sandbox.
// It checks attachment modes (auto:all, sandbox:name, tag:value).
func (s *Store) ListForSandbox(ctx context.Context, sandboxName string, sandboxTags []string) ([]*Integration, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var matched []*Integration
	for _, integ := range all {
		if integ.MatchesSandbox(sandboxName, sandboxTags) {
			matched = append(matched, integ)
		}
	}
	return matched, nil
}

// Delete removes an integration by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	result, err := s.store.DB.ExecContext(ctx,
		`DELETE FROM integrations WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete integration: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) scanIntegration(row *sql.Row) (*Integration, error) {
	var integ Integration
	var iType, attachMode, encSecret, createdAt, updatedAt string
	err := row.Scan(
		&integ.ID, &integ.Name, &iType, &integ.Target, &encSecret,
		&integ.SecretType, &integ.SecretHeader, &integ.Username, &integ.Provider,
		&attachMode, &integ.AttachSelector, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan integration: %w", err)
	}
	integ.Type = IntegrationType(iType)
	integ.AttachMode = AttachmentMode(attachMode)
	secret, err := s.decrypt(encSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	integ.Secret = secret
	integ.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	integ.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &integ, nil
}

func (s *Store) scanIntegrationFromRows(rows *sql.Rows) (*Integration, error) {
	var integ Integration
	var iType, attachMode, encSecret, createdAt, updatedAt string
	err := rows.Scan(
		&integ.ID, &integ.Name, &iType, &integ.Target, &encSecret,
		&integ.SecretType, &integ.SecretHeader, &integ.Username, &integ.Provider,
		&attachMode, &integ.AttachSelector, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan integration: %w", err)
	}
	integ.Type = IntegrationType(iType)
	integ.AttachMode = AttachmentMode(attachMode)
	secret, err := s.decrypt(encSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	integ.Secret = secret
	integ.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	integ.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &integ, nil
}

// encrypt encrypts a plaintext string using AES-GCM.
func (s *Store) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// decrypt decrypts a hex-encoded AES-GCM ciphertext.
func (s *Store) decrypt(encHex string) (string, error) {
	data, err := hex.DecodeString(encHex)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// isDuplicateKey checks if a SQLite error is a unique constraint violation.
func isDuplicateKey(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE constraint failed") ||
		contains(err.Error(), "duplicate key"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
