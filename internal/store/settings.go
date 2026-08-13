package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/kevinnguyen/diaper-raffle/internal/auth"
)

// Setting keys.
const (
	settingPasswordHash = "auth.password_hash"
	settingSessionKey   = "auth.session_key"
)

// Setting reads one value. Missing keys come back as an empty string rather
// than an error, because "not configured yet" is a normal state here.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting writes one value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now())
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// SetPassword stores the shared password as a one-way hash. The plaintext is
// never written anywhere.
func (s *Store) SetPassword(ctx context.Context, password string) error {
	if password == "" {
		return fmt.Errorf("%w: the password cannot be empty", ErrInvalid)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, settingPasswordHash, hash)
}

// ClearPassword takes the lock off, making the site open again.
func (s *Store) ClearPassword(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, settingPasswordHash)
	if err != nil {
		return fmt.Errorf("clear password: %w", err)
	}
	return nil
}

// PasswordHash returns the stored hash, or "" when no password is set.
func (s *Store) PasswordHash(ctx context.Context) (string, error) {
	return s.Setting(ctx, settingPasswordHash)
}

// SessionKey returns the secret used to sign session cookies, generating and
// storing one the first time it is asked for.
//
// It lives in the database rather than in memory so that a restart does not
// sign everybody out — the app is behind a KeepAlive launch agent, and a
// crash mid-party should not send guests back to a password prompt.
func (s *Store) SessionKey(ctx context.Context) ([]byte, error) {
	encoded, err := s.Setting(ctx, settingSessionKey)
	if err != nil {
		return nil, err
	}
	if encoded != "" {
		key, err := base64.RawStdEncoding.DecodeString(encoded)
		if err == nil && len(key) >= 32 {
			return key, nil
		}
		// A corrupt key is not worth failing over; rotating it just means
		// everybody signs in again.
	}

	key, err := auth.NewSessionKey()
	if err != nil {
		return nil, err
	}
	if err := s.SetSetting(ctx, settingSessionKey,
		base64.RawStdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

// RotateSessionKey invalidates every existing session. Used when the password
// changes: somebody who signed in with the old password should not stay in.
func (s *Store) RotateSessionKey(ctx context.Context) error {
	key, err := auth.NewSessionKey()
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, settingSessionKey, base64.RawStdEncoding.EncodeToString(key))
}
