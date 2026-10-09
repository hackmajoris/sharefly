package share

import (
	"crypto/rand"
	"encoding/hex"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

func NewID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = idAlphabet[b[i]%32]
	}
	return string(b), nil
}

// NewToken returns a random 128-bit secret, hex-encoded.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
