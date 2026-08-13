package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("bottles and blankets")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := VerifyPassword(hash, "bottles and blankets")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("the right password was rejected")
	}

	for _, wrong := range []string{
		"bottles and blanket",   // one character short
		"bottles and blankets ", // trailing space
		"Bottles and blankets",  // different case
		"",
	} {
		ok, err := VerifyPassword(hash, wrong)
		if err != nil {
			t.Fatalf("verify %q: %v", wrong, err)
		}
		if ok {
			t.Errorf("%q was accepted", wrong)
		}
	}
}

// The stored hash must not contain the password, and two hashes of the same
// password must differ — otherwise a stolen database reveals which guests
// share a password with another site.
func TestHashIsSaltedAndOpaque(t *testing.T) {
	password := "diaper"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if first == second {
		t.Error("two hashes of the same password are identical — the salt is not random")
	}
	if strings.Contains(first, password) {
		t.Error("the hash contains the password")
	}
	if !strings.HasPrefix(first, "pbkdf2$sha256$") {
		t.Errorf("unexpected hash format: %s", first)
	}

	// Each hash still verifies against its own salt.
	for i, h := range []string{first, second} {
		ok, err := VerifyPassword(h, password)
		if err != nil || !ok {
			t.Errorf("hash %d did not verify: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	for _, stored := range []string{
		"",
		"not-a-hash",
		"pbkdf2$sha256$600000$onlyfourparts",
		"pbkdf2$sha512$600000$c2FsdA$aGFzaA", // wrong digest
		"scrypt$sha256$600000$c2FsdA$aGFzaA", // wrong kdf
		"pbkdf2$sha256$zero$c2FsdA$aGFzaA",   // unparseable iterations
		"pbkdf2$sha256$0$c2FsdA$aGFzaA",      // nonsense iterations
		"pbkdf2$sha256$600000$!!!$aGFzaA",    // bad base64
	} {
		if _, err := VerifyPassword(stored, "anything"); err == nil {
			t.Errorf("%q was accepted as a hash", stored)
		}
	}
}

func TestSessionRoundTrip(t *testing.T) {
	key, err := NewSessionKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	now := time.Now()

	token := IssueSession(key, now, time.Hour)
	if !ValidSession(key, token, now) {
		t.Error("a token was rejected immediately after being issued")
	}
	if !ValidSession(key, token, now.Add(59*time.Minute)) {
		t.Error("a token expired early")
	}
	if ValidSession(key, token, now.Add(2*time.Hour)) {
		t.Error("an expired token was accepted")
	}
}

// A token has to be worthless without the key, or anybody could mint one.
func TestSessionRejectsForgeries(t *testing.T) {
	key, _ := NewSessionKey()
	other, _ := NewSessionKey()
	now := time.Now()
	token := IssueSession(key, now, time.Hour)

	if ValidSession(other, token, now) {
		t.Error("a token signed by a different key was accepted")
	}

	for _, forged := range []string{
		"",
		"nodot",
		".",
		"9999999999.",
		"9999999999.aaaa",
		// The payload swapped for a distant expiry, keeping a real signature.
		strings.Replace(token, strings.Split(token, ".")[0], "9999999999", 1),
	} {
		if ValidSession(key, forged, now) {
			t.Errorf("forged token %q was accepted", forged)
		}
	}

	// Rotating the key must invalidate everything already issued.
	rotated, _ := NewSessionKey()
	if ValidSession(rotated, token, now) {
		t.Error("a token survived a key rotation")
	}
}
