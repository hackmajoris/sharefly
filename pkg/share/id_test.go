package share

import (
	"strings"
	"testing"
)

func TestNewIDFormat(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 10 {
		t.Fatalf("len(%q) = %d, want 10", id, len(id))
	}
	for _, c := range id {
		if !strings.ContainsRune(idAlphabet, c) {
			t.Fatalf("id %q contains %q outside lowercase base32", id, c)
		}
	}
}

// The ID is the only access control for a share, so it must be random, not merely well-formed.
func TestNewIDUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 10000 {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
