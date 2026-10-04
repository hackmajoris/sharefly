package share

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

var ErrNotFound = errors.New("share not found")

type Share struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Entry     string     `json:"entry"`
	Size      int64      `json:"size"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	// PasswordHash, when set, makes the public file server require the share's password.
	PasswordHash string `json:"password_hash,omitempty"`
}

// Link is a share as the API returns it: never the password hash, the password only once, from the upload.
type Link struct {
	Share
	URL       string `json:"url"`
	Protected bool   `json:"protected"`
	Password  string `json:"password,omitempty"`
}

type Store struct {
	mu     sync.Mutex
	path   string
	shares []Share
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.shares); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Add(sh Share) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(append(slices.Clone(s.shares), sh))
}

func (s *Store) Get(id string) (Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return Share{}, ErrNotFound
	}
	return s.shares[i], nil
}

func (s *Store) List() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.shares)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return ErrNotFound
	}
	return s.commit(slices.Delete(slices.Clone(s.shares), i, i+1))
}

func (s *Store) Renew(id string, d time.Duration, never bool, now time.Time) (Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 || (s.shares[i].ExpiresAt != nil && !s.shares[i].ExpiresAt.After(now)) {
		return Share{}, ErrNotFound
	}
	next := slices.Clone(s.shares)
	next[i].ExpiresAt = nil
	if !never {
		exp := now.Add(d)
		next[i].ExpiresAt = &exp
	}
	if err := s.commit(next); err != nil {
		return Share{}, err
	}
	return next[i], nil
}

func (s *Store) index(id string) int {
	return slices.IndexFunc(s.shares, func(sh Share) bool { return sh.ID == id })
}

func (s *Store) commit(next []Share) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	s.shares = next
	return nil
}
