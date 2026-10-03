package share

import "crypto/rand"

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
