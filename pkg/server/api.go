package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackmajoris/go-share/pkg/share"
)

const defaultTTL = "7d"

type API struct {
	Store     *share.Store
	DataDir   string
	PublicURL string
	MaxBytes  int64
}

type shareResponse struct {
	share.Share
	URL string `json:"url"`
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /shares", a.upload)
	mux.HandleFunc("GET /shares", a.list)
	mux.HandleFunc("DELETE /shares/{id}", a.delete)
	mux.HandleFunc("POST /shares/{id}/renew", a.renew)
	return mux
}

func (a *API) sharesDir() string { return filepath.Join(a.DataDir, "shares") }
func (a *API) tmpDir() string    { return filepath.Join(a.DataDir, "tmp") }

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
	id, err := a.newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp := filepath.Join(a.tmpDir(), id)
	defer os.RemoveAll(tmp)

	body := http.MaxBytesReader(w, r.Body, a.MaxBytes)
	size, err := share.Extract(body, tmp, a.MaxBytes)
	var maxErr *http.MaxBytesError
	if errors.Is(err, share.ErrTooLarge) || errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
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
	dst := filepath.Join(a.sharesDir(), id)
	if err := os.Rename(tmp, dst); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	if err := a.Store.Add(sh); err != nil {
		os.RemoveAll(dst)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a.response(sh))
}

func (a *API) newID() (string, error) {
	for range 5 {
		id, err := share.NewID()
		if err != nil {
			return "", err
		}
		if _, err := a.Store.Get(id); err == nil {
			continue
		}
		if exists(filepath.Join(a.sharesDir(), id)) || exists(filepath.Join(a.tmpDir(), id)) {
			continue
		}
		return id, nil
	}
	return "", errors.New("could not generate a unique id")
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	shares := a.Store.List()
	out := make([]shareResponse, 0, len(shares))
	for _, sh := range shares {
		out = append(out, a.response(sh))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.Store.Get(id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := os.RemoveAll(filepath.Join(a.sharesDir(), id)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.Store.Delete(id); err != nil {
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
	if _, _, err := share.ParseTTL(req.TTL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sh, err := a.Store.Renew(r.PathValue("id"), req.TTL, time.Now().UTC())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.response(sh))
}

func (a *API) response(sh share.Share) shareResponse {
	path := (&url.URL{Path: "/" + sh.ID + "/" + sh.Entry}).EscapedPath()
	return shareResponse{Share: sh, URL: strings.TrimSuffix(a.PublicURL, "/") + path}
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, share.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
