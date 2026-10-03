package share

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shares.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, _ := newStore(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List() = %v, want empty", got)
	}
}

// Shares must outlive a server restart; otherwise the sweep could never remove their dirs.
func TestStorePersistsAcrossReopen(t *testing.T) {
	s, path := newStore(t)
	exp := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	want := Share{ID: "k7f3x9qa2m", Name: "report.html", Entry: "report.html", Size: 20480,
		CreatedAt: time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC), ExpiresAt: &exp}
	if err := s.Add(want); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Share{ID: "neverexpir", Name: "site"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Entry != want.Entry || got.Size != want.Size ||
		!got.CreatedAt.Equal(want.CreatedAt) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	never, err := reopened.Get("neverexpir")
	if err != nil {
		t.Fatal(err)
	}
	if never.ExpiresAt != nil {
		t.Fatalf("never share ExpiresAt = %v, want nil", never.ExpiresAt)
	}
	if n := len(reopened.List()); n != 2 {
		t.Fatalf("List() len = %d, want 2", n)
	}
}

func TestStoreDeletePersists(t *testing.T) {
	s, path := newStore(t)
	for _, id := range []string{"a", "b"} {
		if err := s.Add(Share{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted share still present after reopen: %v", err)
	}
	if _, err := reopened.Get("b"); err != nil {
		t.Fatalf("unrelated share lost: %v", err)
	}
}

func TestStoreRenew(t *testing.T) {
	s, path := newStore(t)
	if err := s.Add(Share{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)

	got, err := s.Renew("a", "7d", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(7 * 24 * time.Hour); got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}

	got, err = s.Renew("a", "never", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("never: ExpiresAt = %v, want nil", got.ExpiresAt)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if sh, _ := reopened.Get("a"); sh.ExpiresAt != nil {
		t.Fatalf("never renewal not persisted: %v", sh.ExpiresAt)
	}
}

func TestStoreRenewInvalidTTL(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Add(Share{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Renew("a", "7w", time.Now()); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Renew with bad ttl: err = %v, want ttl error", err)
	}
}

func TestStoreUnknownID(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, want ErrNotFound", err)
	}
	if _, err := s.Renew("nope", "7d", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Renew: %v, want ErrNotFound", err)
	}
}

// A corrupt file must stop the server from starting; treating it as empty would forget every
// share and leave their dirs public with nothing to expire them.
func TestOpenCorruptFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open on corrupt file: want error")
	}
}

// A failed save must not leave memory ahead of disk, or the API would report changes a restart loses.
func TestStoreFailedSaveKeepsMemoryUnchanged(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "missing", "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Share{ID: "a"}); err == nil {
		t.Fatal("Add into nonexistent dir: want error")
	}
	if n := len(s.List()); n != 0 {
		t.Fatalf("List() len = %d after failed save, want 0", n)
	}
}
