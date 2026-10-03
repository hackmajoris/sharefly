package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupShares(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "site123456", "index.html"), "site index")
	writeFile(t, filepath.Join(dir, "site123456", "assets", "app.js"), "js")
	writeFile(t, filepath.Join(dir, "single1234", "report.html"), "report")
	return dir
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Directory listings would let anyone enumerate share IDs and files, defeating
// the unguessable-ID access control.
func TestFilesNeverListsDirectories(t *testing.T) {
	h := FilesHandler(setupShares(t))
	for _, p := range []string{"/", "/single1234/", "/single1234", "/site123456/assets/", "/site123456/assets"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "single1234") || strings.Contains(body, "site123456") ||
			strings.Contains(body, "report.html") || strings.Contains(body, "app.js") {
			t.Errorf("%s: body leaks listing: %q", p, body)
		}
	}
}

func TestFilesServesShareContent(t *testing.T) {
	h := FilesHandler(setupShares(t))
	cases := map[string]string{
		"/site123456/":              "site index",
		"/site123456/assets/app.js": "js",
		"/single1234/report.html":   "report",
	}
	for p, want := range cases {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s: got %d %q, want 200 %q", p, rec.Code, rec.Body.String(), want)
		}
	}
}

// Without no-store, Cloudflare's edge would keep serving cached content after
// rm or expiry, so every response (including 404s and redirects) must carry it.
func TestFilesSetsNoStoreOnEveryResponse(t *testing.T) {
	h := FilesHandler(setupShares(t))
	for _, p := range []string{"/", "/site123456/", "/site123456", "/site123456/assets/app.js", "/single1234/report.html", "/missing/x"} {
		rec := get(t, h, p)
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s (status %d): Cache-Control %q", p, rec.Code, got)
		}
	}
}

// A directory named index.html is not a page; treating it as one fell back to a listing.
func TestFilesNoListingWhenIndexHTMLIsADirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "abc1234567", "index.html", "x.txt"), "x")
	writeFile(t, filepath.Join(dir, "abc1234567", "sub", "index.html", "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "abc1234567", "secret.txt"), "s")
	h := FilesHandler(dir)
	for _, p := range []string{"/abc1234567/", "/abc1234567/sub/"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "secret.txt") || strings.Contains(body, "index.html") {
			t.Errorf("%s: body leaks listing: %q", p, body)
		}
	}
}

// shares.json sits next to shares/ and lists every share ID; it must never be reachable.
func TestFilesRejectsTraversal(t *testing.T) {
	data := t.TempDir()
	writeFile(t, filepath.Join(data, "shares.json"), `[{"id":"leakedid00"}]`)
	writeFile(t, filepath.Join(data, "shares", "site123456", "index.html"), "i")
	h := FilesHandler(filepath.Join(data, "shares"))
	for _, p := range []string{"/../shares.json", "/%2e%2e/shares.json", "/site123456/../../shares.json", "/..%2fshares.json"} {
		if body := get(t, h, p).Body.String(); strings.Contains(body, "leakedid00") {
			t.Errorf("%s: served shares.json", p)
		}
	}
}
