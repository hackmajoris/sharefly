package share

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

	got, err := s.Renew("a", 7*24*time.Hour, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(7 * 24 * time.Hour); got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}

	got, err = s.Renew("a", 0, true, now)
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
	sh, err := reopened.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sh.ExpiresAt != nil {
		t.Fatalf("never renewal not persisted: %v", sh.ExpiresAt)
	}
}

// Sweep deletes from a snapshot without re-checking, so renewing an already-expired share must
// fail; otherwise renew could report success and the share be deleted right after.
func TestStoreRenewRejectsExpiredShare(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, sh := range []Share{{ID: "expired", ExpiresAt: &now}, {ID: "live", ExpiresAt: ptr(now.Add(time.Second))}} {
		if err := s.Add(sh); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Renew("expired", time.Hour, false, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Renew expired: err = %v, want ErrNotFound", err)
	}
	if sh, _ := s.Get("expired"); sh.ExpiresAt == nil || !sh.ExpiresAt.Equal(now) {
		t.Fatalf("expired share's expiry changed: %v", sh.ExpiresAt)
	}
	if _, err := s.Renew("live", time.Hour, false, now); err != nil {
		t.Fatalf("Renew live: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestStoreUnknownID(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, want ErrNotFound", err)
	}
	if _, err := s.Renew("nope", time.Hour, false, time.Now()); !errors.Is(err, ErrNotFound) {
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

// Delete and Renew must also leave memory unchanged when the save fails.
func TestStoreFailedDeleteAndRenewKeepMemoryUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	s, path := newStore(t)
	if err := s.Add(Share{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := s.Delete("a"); err == nil {
		t.Fatal("Delete with unwritable dir: want error")
	}
	if _, err := s.Renew("a", time.Hour, false, time.Now()); err == nil {
		t.Fatal("Renew with unwritable dir: want error")
	}
	sh, err := s.Get("a")
	if err != nil {
		t.Fatalf("record dropped from memory after failed Delete: %v", err)
	}
	if sh.ExpiresAt != nil {
		t.Fatalf("expiry changed in memory after failed Renew: %v", sh.ExpiresAt)
	}
}

// The API calls the store from concurrent requests; no mutation may be lost.
func TestStoreConcurrentMutations(t *testing.T) {
	s, path := newStore(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			id := fmt.Sprintf("id%02d", i)
			if err := s.Add(Share{ID: id}); err != nil {
				t.Error(err)
				return
			}
			if i%2 == 0 {
				if err := s.Delete(id); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reopened.List()); n != 10 {
		t.Fatalf("persisted %d shares, want 10", n)
	}
}

// A once share must open for exactly one caller, even when two visitors click at the same time; the second
// would otherwise get its own cookie and a second view.
func TestStoreOpenOnlyOnce(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Add(Share{ID: "once", Once: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	opened := make(chan string, 10)
	for i := range 10 {
		wg.Go(func() {
			if _, err := s.Open("once", fmt.Sprint("token", i), now); err == nil {
				opened <- fmt.Sprint("token", i)
			}
		})
	}
	wg.Wait()
	close(opened)
	var winners []string
	for tok := range opened {
		winners = append(winners, tok)
	}
	if len(winners) != 1 {
		t.Fatalf("%d callers opened the share, want 1", len(winners))
	}
	if sh, _ := s.Get("once"); sh.OpenToken != winners[0] {
		t.Fatalf("stored token %q, want the winner's %q", sh.OpenToken, winners[0])
	}
}

// Opening starts the visitor's grace minute, but must never extend a share that expires sooner.
func TestStoreOpenCutsExpiry(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	soon := now.Add(10 * time.Second)
	for _, sh := range []Share{
		{ID: "never", Once: true},
		{ID: "week", Once: true, ExpiresAt: ptr(now.Add(7 * 24 * time.Hour))},
		{ID: "soon", Once: true, ExpiresAt: &soon},
	} {
		if err := s.Add(sh); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]time.Time{"never": now.Add(OpenGrace), "week": now.Add(OpenGrace), "soon": soon} {
		sh, err := s.Open(id, "tok", now)
		if err != nil {
			t.Fatalf("Open %s: %v", id, err)
		}
		if sh.ExpiresAt == nil || !sh.ExpiresAt.Equal(want) {
			t.Errorf("%s: expires %v, want %v", id, sh.ExpiresAt, want)
		}
	}
}

// Only unexpired once shares can be opened; anything else would hand out a cookie for a share that is gone
// or was never one-time.
func TestStoreOpenRejects(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, sh := range []Share{{ID: "plain"}, {ID: "expired", Once: true, ExpiresAt: &now}} {
		if err := s.Add(sh); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"plain", "expired", "missing"} {
		if _, err := s.Open(id, "tok", now); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open %s: err = %v, want ErrNotFound", id, err)
		}
	}
}
