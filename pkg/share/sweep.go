package share

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

func Sweep(store *Store, sharesDir string, now time.Time) error {
	var errs []error
	for _, sh := range store.List() {
		if sh.ExpiresAt == nil || sh.ExpiresAt.After(now) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(sharesDir, sh.ID)); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := store.Delete(sh.ID); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func Reconcile(store *Store, sharesDir, tmpDir string) error {
	known := map[string]bool{}
	for _, sh := range store.List() {
		known[sh.ID] = true
	}
	entries, err := os.ReadDir(sharesDir)
	if err != nil {
		return err
	}
	var errs []error
	present := map[string]bool{}
	for _, e := range entries {
		if known[e.Name()] && e.IsDir() {
			present[e.Name()] = true
			continue
		}
		if err := os.RemoveAll(filepath.Join(sharesDir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	for id := range known {
		if present[id] {
			continue
		}
		if err := store.Delete(id); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	tmpEntries, err := os.ReadDir(tmpDir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, e := range tmpEntries {
		if err := os.RemoveAll(filepath.Join(tmpDir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
