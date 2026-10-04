package share

import (
	"regexp"
	"testing"
)

// Only the generated password's entropy (80 bits) makes the fast hash and the lack of rate limits safe.
func TestNewPasswordIsRandomAndChecks(t *testing.T) {
	pw, hash, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z2-7]{4}(-[a-z2-7]{4}){3}$`).MatchString(pw) {
		t.Fatalf("password %q: want 16 base32 characters in groups of 4", pw)
	}
	if hash == pw || len(hash) != 64 {
		t.Fatalf("hash %q must be a SHA-256 hex digest, never the password", hash)
	}
	if !CheckPassword(hash, pw) || CheckPassword(hash, pw+"x") || CheckPassword(hash, "") {
		t.Fatal("CheckPassword must accept only the exact password")
	}
	if pw2, _, _ := NewPassword(); pw2 == pw {
		t.Fatal("two passwords are equal")
	}
}
