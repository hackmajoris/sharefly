package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackmajoris/go-share/pkg/share"
)

type tarEntry struct {
	hdr  tar.Header
	body string
}

func regular(name, body string) tarEntry {
	return tarEntry{tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}, body}
}

func tarGz(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&e.hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func newTestAPI(t *testing.T, maxBytes int64) *API {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"shares", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := share.Open(filepath.Join(dir, "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &API{Store: store, DataDir: dir, PublicURL: "https://share.example.com/", MaxBytes: maxBytes}
}

func do(t *testing.T, a *API, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, body))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAPIRoundTrip(t *testing.T) {
	a := newTestAPI(t, 1<<20)

	rec := do(t, a, http.MethodPost, "/shares?ttl=1h&name=site", tarGz(t,
		regular("index.html", "hello"), regular("css/app.css", "body{}")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	up := decode[share.Link](t, rec)
	if up.Name != "site" || up.Entry != "" || up.Size != int64(len("hello")+len("body{}")) {
		t.Fatalf("unexpected record %+v", up)
	}
	if up.URL != "https://share.example.com/"+up.ID+"/" {
		t.Fatalf("url = %q", up.URL)
	}
	if up.ExpiresAt == nil || up.ExpiresAt.Sub(up.CreatedAt) != time.Hour {
		t.Fatalf("expires_at = %v, created_at = %v", up.ExpiresAt, up.CreatedAt)
	}
	shareDir := filepath.Join(a.DataDir, "shares", up.ID)
	if got, err := os.ReadFile(filepath.Join(shareDir, "index.html")); err != nil || string(got) != "hello" {
		t.Fatalf("index.html = %q, %v", got, err)
	}
	if names := dirEntries(t, filepath.Join(a.DataDir, "tmp")); len(names) != 0 {
		t.Fatalf("tmp not empty after upload: %v", names)
	}

	rec = do(t, a, http.MethodGet, "/shares", nil)
	list := decode[[]share.Link](t, rec)
	if rec.Code != http.StatusOK || len(list) != 1 || list[0].ID != up.ID || list[0].URL != up.URL {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	rec = do(t, a, http.MethodPost, "/shares/"+up.ID+"/renew", strings.NewReader(`{"ttl":"never"}`))
	renewed := decode[share.Link](t, rec)
	if rec.Code != http.StatusOK || renewed.ExpiresAt != nil || renewed.URL != up.URL {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body)
	}

	rec = do(t, a, http.MethodDelete, "/shares/"+up.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(shareDir); !os.IsNotExist(err) {
		t.Fatalf("share dir still on disk after delete: %v", err)
	}
	if list := decode[[]share.Link](t, do(t, a, http.MethodGet, "/shares", nil)); len(list) != 0 {
		t.Fatalf("record still listed after delete: %v", list)
	}
}

func TestAPIUploadSingleFileEntryAndDefaultTTL(t *testing.T) {
	a := newTestAPI(t, 1<<20)
	rec := do(t, a, http.MethodPost, "/shares?name=report.html", tarGz(t, regular("report.html", "r")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	up := decode[share.Link](t, rec)
	if up.Entry != "report.html" || up.URL != "https://share.example.com/"+up.ID+"/report.html" {
		t.Fatalf("unexpected record %+v", up)
	}
	if up.ExpiresAt == nil || up.ExpiresAt.Sub(up.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("default ttl not 7d: %v", up.ExpiresAt)
	}
}

// Every rejected upload must leave nothing public in shares/ and nothing behind in tmp/.
func TestAPIUploadRejections(t *testing.T) {
	big := strings.Repeat("x", 2048)
	tests := []struct {
		name string
		path string
		body *bytes.Buffer
		code int
	}{
		{"bad ttl", "/shares?ttl=7w", tarGz(t, regular("index.html", "x")), http.StatusBadRequest},
		{"traversal", "/shares", tarGz(t, regular("../../evil.html", "x")), http.StatusBadRequest},
		{"absolute path", "/shares", tarGz(t, regular("/abs.html", "x")), http.StatusBadRequest},
		{"hardlink", "/shares", tarGz(t, tarEntry{hdr: tar.Header{Name: "hl", Typeflag: tar.TypeLink, Linkname: "../../shares.json"}}), http.StatusBadRequest},
		{"fifo", "/shares", tarGz(t, tarEntry{hdr: tar.Header{Name: "pipe", Typeflag: tar.TypeFifo}}), http.StatusBadRequest},
		{"symlink", "/shares", tarGz(t, tarEntry{hdr: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}), http.StatusBadRequest},
		{"not gzip", "/shares", bytes.NewBufferString("plain"), http.StatusBadRequest},
		{"multi file without index", "/shares", tarGz(t, regular("a.html", "a"), regular("b.html", "b")), http.StatusBadRequest},
		{"uncompressed over cap", "/shares", tarGz(t, regular("index.html", big)), http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t, 1024)
			rec := do(t, a, http.MethodPost, tt.path, tt.body)
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d (%s)", rec.Code, tt.code, rec.Body)
			}
			if e := decode[map[string]string](t, rec); e["error"] == "" {
				t.Fatalf("missing error message: %s", rec.Body)
			}
			if names := dirEntries(t, filepath.Join(a.DataDir, "shares")); len(names) != 0 {
				t.Fatalf("shares/ not empty: %v", names)
			}
			if names := dirEntries(t, filepath.Join(a.DataDir, "tmp")); len(names) != 0 {
				t.Fatalf("tmp/ not empty: %v", names)
			}
			if _, err := os.Stat(filepath.Join(a.DataDir, "evil.html")); !os.IsNotExist(err) {
				t.Fatal("traversal wrote outside the share dir")
			}
			if len(a.Store.List()) != 0 {
				t.Fatal("record added for rejected upload")
			}
		})
	}
}

// The request body cap must hold even when the compressed stream itself is oversized.
func TestAPIUploadCompressedBodyOverCap(t *testing.T) {
	a := newTestAPI(t, 64)
	rec := do(t, a, http.MethodPost, "/shares", tarGz(t, regular("index.html", "small")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413 (%s)", rec.Code, rec.Body)
	}
}

func TestAPIUnknownID(t *testing.T) {
	a := newTestAPI(t, 1024)
	if rec := do(t, a, http.MethodDelete, "/shares/nosuchid00", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(t, a, http.MethodPost, "/shares/nosuchid00/renew", strings.NewReader(`{"ttl":"1d"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("renew: %d", rec.Code)
	}
}

func TestAPIRenewBadTTL(t *testing.T) {
	a := newTestAPI(t, 1024)
	up := decode[share.Link](t, do(t, a, http.MethodPost, "/shares", tarGz(t, regular("index.html", "x"))))
	for _, body := range []string{`{"ttl":"7w"}`, `{}`, `not json`} {
		if rec := do(t, a, http.MethodPost, "/shares/"+up.ID+"/renew", strings.NewReader(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("renew %s: %d", body, rec.Code)
		}
	}
}

// The public link is built from the stored entry, so names with spaces must be escaped to work.
func TestAPIURLEscapesEntryAndTrimsPublicURL(t *testing.T) {
	a := newTestAPI(t, 1024)
	a.PublicURL = "https://share.example.com"
	up := decode[share.Link](t, do(t, a, http.MethodPost, "/shares", tarGz(t, regular("my report.html", "r"))))
	if want := "https://share.example.com/" + up.ID + "/my%20report.html"; up.URL != want {
		t.Fatalf("url = %q, want %q", up.URL, want)
	}
}

// Delete removes the dir before the record: if the dir can't be removed the record must stay,
// so the still-public share remains listed and deletable.
func TestAPIDeleteKeepsRecordWhenDirRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	a := newTestAPI(t, 1024)
	up := decode[share.Link](t, do(t, a, http.MethodPost, "/shares", tarGz(t, regular("index.html", "x"))))
	sharesDir := filepath.Join(a.DataDir, "shares")
	if err := os.Chmod(sharesDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sharesDir, 0o755) })

	rec := do(t, a, http.MethodDelete, "/shares/"+up.ID, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s, want 500", rec.Code, rec.Body)
	}
	if _, err := a.Store.Get(up.ID); err != nil {
		t.Fatalf("record dropped while dir is still public: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharesDir, up.ID)); err != nil {
		t.Fatalf("share dir: %v", err)
	}
}

// If the record can't be saved, the extracted dir must not stay public with nothing to expire it.
func TestAPIUploadRemovesDirWhenRecordSaveFails(t *testing.T) {
	a := newTestAPI(t, 1024)
	store, err := share.Open(filepath.Join(a.DataDir, "missing", "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.Store = store
	rec := do(t, a, http.MethodPost, "/shares", tarGz(t, regular("index.html", "x")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("upload: %d %s, want 500", rec.Code, rec.Body)
	}
	if names := dirEntries(t, filepath.Join(a.DataDir, "shares")); len(names) != 0 {
		t.Fatalf("unrecorded share left public: %v", names)
	}
}

// Server-side filesystem failures are not the client's fault (500), and must not leak server paths.
func TestAPIUploadFilesystemErrorIs500WithoutPaths(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	a := newTestAPI(t, 1024)
	tmpDir := filepath.Join(a.DataDir, "tmp")
	if err := os.Chmod(tmpDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(tmpDir, 0o755) })

	rec := do(t, a, http.MethodPost, "/shares", tarGz(t, regular("index.html", "x")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("upload: %d %s, want 500", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), a.DataDir) {
		t.Fatalf("error leaks server path: %s", rec.Body)
	}
}
