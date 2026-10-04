package share

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// NewPassword returns a random password (16 characters, 80 bits, grouped as xxxx-xxxx-xxxx-xxxx) and its hash.
// The password is generated, never chosen, so its entropy makes a fast hash safe and guessing over HTTP hopeless.
func NewPassword() (password, hash string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(idAlphabet[c%32])
	}
	password = sb.String()
	return password, hashPassword(password), nil
}

// CheckPassword reports whether password matches hash, in constant time.
func CheckPassword(hash, password string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(hashPassword(password))) == 1
}

func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}
