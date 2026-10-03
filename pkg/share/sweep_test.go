package share

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkShareDir(t *testing.T, sharesDir, id string) string {
	t.Helper()
	dir := filepath.Join(sharesDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// Expired content must stop being public, while live and never-expiring shares stay reachable.
func TestSweepRemovesOnlyExpiredShares(t *testing.T) {
	s, _ := newStore(t)
	sharesDir := t.TempDir()
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	for _, sh := range []Share{{ID: "expired", ExpiresAt: &past}, {ID: "exactnow", ExpiresAt: &now},
		{ID: "live", ExpiresAt: &future}, {ID: "never"}} {
		if err := s.Add(sh); err != nil {
			t.Fatal(err)
		}
		mkShareDir(t, sharesDir, sh.ID)
	}

	if err := Sweep(s, sharesDir, now); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"expired", "exactnow"} {
		if exists(t, filepath.Join(sharesDir, id)) {
			t.Errorf("%s dir still exists after sweep", id)
		}
		if _, err := s.Get(id); err == nil {
			t.Errorf("%s record still present after sweep", id)
		}
	}
	for _, id := range []string{"live", "never"} {
		if !exists(t, filepath.Join(sharesDir, id, "index.html")) {
			t.Errorf("%s dir removed by sweep", id)
		}
		if _, err := s.Get(id); err != nil {
			t.Errorf("%s record removed by sweep: %v", id, err)
		}
	}
}

// If the dir can't be removed, the record must survive so the next sweep retries
// instead of forgetting a still-public dir.
func TestSweepKeepsRecordWhenDirRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	s, _ := newStore(t)
	sharesDir := t.TempDir()
	now := time.Now()
	past := now.Add(-time.Hour)
	if err := s.Add(Share{ID: "stuck", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	mkShareDir(t, sharesDir, "stuck")
	if err := os.Chmod(sharesDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sharesDir, 0o755) })

	if err := Sweep(s, sharesDir, now); err == nil {
		t.Fatal("Sweep() error = nil, want removal error")
	}
	if _, err := s.Get("stuck"); err != nil {
		t.Fatalf("record dropped despite failed removal: %v", err)
	}
}

// An unlisted dir is a public leak nobody can rm; a record without a dir is a dead link; tmp holds
// half-finished uploads from a crash.
func TestReconcile(t *testing.T) {
	s, _ := newStore(t)
	sharesDir, tmpDir := t.TempDir(), t.TempDir()
	for _, id := range []string{"keep", "nodir"} {
		if err := s.Add(Share{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	mkShareDir(t, sharesDir, "keep")
	mkShareDir(t, sharesDir, "orphan")
	if err := os.WriteFile(filepath.Join(sharesDir, "stray.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkShareDir(t, tmpDir, "partial")

	if err := Reconcile(s, sharesDir, tmpDir); err != nil {
		t.Fatal(err)
	}

	if !exists(t, filepath.Join(sharesDir, "keep", "index.html")) {
		t.Error("recorded share dir removed")
	}
	if _, err := s.Get("keep"); err != nil {
		t.Errorf("recorded share dropped: %v", err)
	}
	if exists(t, filepath.Join(sharesDir, "orphan")) {
		t.Error("orphan dir not removed")
	}
	if exists(t, filepath.Join(sharesDir, "stray.html")) {
		t.Error("stray file not removed")
	}
	if _, err := s.Get("nodir"); err == nil {
		t.Error("record without dir not dropped")
	}
	if entries, err := os.ReadDir(tmpDir); err != nil || len(entries) != 0 {
		t.Errorf("tmp not emptied: %v, %v", entries, err)
	}
	if !exists(t, tmpDir) {
		t.Error("tmp dir itself removed")
	}
}

// One stuck share must not keep other expired shares public.
func TestSweepContinuesPastFailedShare(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	s, _ := newStore(t)
	sharesDir := t.TempDir()
	now := time.Now()
	past := now.Add(-time.Hour)
	for _, id := range []string{"stuck", "other"} {
		if err := s.Add(Share{ID: id, ExpiresAt: &past}); err != nil {
			t.Fatal(err)
		}
	}
	stuck := mkShareDir(t, sharesDir, "stuck")
	mkShareDir(t, sharesDir, "other")
	if err := os.Chmod(stuck, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(stuck, 0o755) })

	if err := Sweep(s, sharesDir, now); err == nil {
		t.Fatal("Sweep() error = nil, want removal error")
	}
	if exists(t, filepath.Join(sharesDir, "other")) {
		t.Error("other expired share still public after a failure on stuck")
	}
	if _, err := s.Get("other"); err == nil {
		t.Error("other record still present")
	}
	if _, err := s.Get("stuck"); err != nil {
		t.Errorf("stuck record dropped despite failed removal: %v", err)
	}
}

// A recorded ID that is a plain file is not a servable share, so both go.
func TestReconcileDropsRecordWhoseIDIsAFile(t *testing.T) {
	s, _ := newStore(t)
	sharesDir, tmpDir := t.TempDir(), t.TempDir()
	if err := s.Add(Share{ID: "fileid"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharesDir, "fileid"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(s, sharesDir, tmpDir); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(sharesDir, "fileid")) {
		t.Error("plain file under shares/ not removed")
	}
	if _, err := s.Get("fileid"); err == nil {
		t.Error("record for plain file not dropped")
	}
}

// A missing data dir must surface as an error instead of a silent no-op.
func TestReconcileMissingDirsFail(t *testing.T) {
	s, _ := newStore(t)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := Reconcile(s, missing, t.TempDir()); err == nil {
		t.Error("missing shares dir: want error")
	}
	if err := Reconcile(s, t.TempDir(), missing); err == nil {
		t.Error("missing tmp dir: want error")
	}
}
