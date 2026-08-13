// Package auth implements the one thing this app needs to keep strangers out:
// a single shared password that the host announces at the party.
//
// There are no accounts. Everybody who is in the room is equally trusted, and
// asking a dozen guests to register would ruin the point of the raffle. What
// this protects against is a stranger who guesses the domain.
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Iterations for the password KDF. The password is checked once per guest per
// party, so this can be slow enough to make offline guessing painful without
// anybody noticing.
const Iterations = 600_000

const (
	saltLength = 16
	keyLength  = 32
)

// ErrMalformedHash means the stored hash is not something this package wrote.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// HashPassword derives a verifier for a password. The result is safe to store:
// it is a one-way function of the password with a random salt, in the shape
//
//	pbkdf2$sha256$<iterations>$<salt>$<key>
//
// which records the parameters alongside the digest, so the cost can be
// raised later without invalidating existing hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read randomness: %w", err)
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, Iterations, keyLength)
	if err != nil {
		return "", fmt.Errorf("auth: derive key: %w", err)
	}

	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s",
		Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the stored hash.
//
// The comparison is constant time. A wrong password must take exactly as long
// to reject as a nearly-right one, or the timing itself leaks the answer.
func VerifyPassword(stored, password string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false, ErrMalformedHash
	}

	iterations, err := strconv.Atoi(parts[2])
	if err != nil || iterations <= 0 {
		return false, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}

	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, fmt.Errorf("auth: derive key: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NewSessionKey makes the secret used to sign session cookies.
func NewSessionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("auth: read randomness: %w", err)
	}
	return key, nil
}

// IssueSession mints a signed token that is good until now+ttl.
//
// The token carries its own expiry and a signature over it, so the server
// keeps no session table. Nothing about a guest is stored; the cookie only
// says "somebody knew the password at this time".
func IssueSession(key []byte, now time.Time, ttl time.Duration) string {
	expiry := now.Add(ttl).Unix()
	payload := strconv.FormatInt(expiry, 10)
	return payload + "." + sign(key, payload)
}

// ValidSession reports whether token was signed by this key and has not
// expired.
func ValidSession(key []byte, token string, now time.Time) bool {
	payload, signature, found := strings.Cut(token, ".")
	if !found {
		return false
	}
	// Check the signature before parsing anything else: an unsigned token's
	// contents are attacker-controlled and should not be acted on at all.
	if !hmac.Equal([]byte(signature), []byte(sign(key, payload))) {
		return false
	}
	expiry, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return now.Unix() < expiry
}

func sign(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
