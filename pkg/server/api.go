package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hackmajoris/go-share/pkg/share"
)

const defaultTTL = "7d"

type API struct {
	Store     *share.Store
	DataDir   string
	PublicURL string
	MaxBytes  int64

	publishMu sync.Mutex
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /shares", a.upload)
	mux.HandleFunc("GET /shares", a.list)
	mux.HandleFunc("DELETE /shares/{id}", a.delete)
	mux.HandleFunc("POST /shares/{id}/renew", a.renew)
	return mux
}

func (a *API) SharesDir() string { return filepath.Join(a.DataDir, "shares") }
func (a *API) TmpDir() string    { return filepath.Join(a.DataDir, "tmp") }

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	ttl := r.URL.Query().Get("ttl")
	if ttl == "" {
		ttl = defaultTTL
	}
	d, never, err := share.ParseTTL(ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := share.NewID()
	if err != nil {
		internalError(w, err)
		return
	}
	tmp := filepath.Join(a.TmpDir(), id)
	defer os.RemoveAll(tmp)

	body := http.MaxBytesReader(w, r.Body, a.MaxBytes)
	size, err := share.Extract(body, tmp, a.MaxBytes)
	var maxErr *http.MaxBytesError
	if errors.Is(err, share.ErrTooLarge) || errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
		return
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		internalError(w, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := share.DetectEntry(tmp)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	sh := share.Share{
		ID:        id,
		Name:      r.URL.Query().Get("name"),
		Entry:     entry,
		Size:      size,
		CreatedAt: now,
	}
	if !never {
		exp := now.Add(d)
		sh.ExpiresAt = &exp
	}
	if err := a.publish(sh, tmp); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.response(sh))
}

func (a *API) publish(sh share.Share, tmp string) error {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()
	if err := a.Store.Add(sh); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(a.SharesDir(), sh.ID)); err != nil {
		if derr := a.Store.Delete(sh.ID); derr != nil {
			log.Printf("drop record %s: %v", sh.ID, derr)
		}
		return err
	}
	return nil
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	shares := a.Store.List()
	out := make([]share.Link, 0, len(shares))
	for _, sh := range shares {
		out = append(out, a.response(sh))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.publishMu.Lock()
	defer a.publishMu.Unlock()
	if _, err := a.Store.Get(id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := os.RemoveAll(filepath.Join(a.SharesDir(), id)); err != nil {
		internalError(w, err)
		return
	}
	if err := a.Store.Delete(id); err != nil && !errors.Is(err, share.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) renew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TTL string `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, never, err := share.ParseTTL(req.TTL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sh, err := a.Store.Renew(r.PathValue("id"), d, never, time.Now().UTC())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.response(sh))
}

func (a *API) response(sh share.Share) share.Link {
	path := (&url.URL{Path: "/" + sh.ID + "/" + sh.Entry}).EscapedPath()
	return share.Link{Share: sh, URL: strings.TrimSuffix(a.PublicURL, "/") + path}
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, share.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	internalError(w, err)
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("api: %v", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
