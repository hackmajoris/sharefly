package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackmajoris/sharefly/pkg/share"
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
	h := FilesHandler(setupShares(t), nil)
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
	h := FilesHandler(setupShares(t), nil)
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
	h := FilesHandler(setupShares(t), nil)
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
	h := FilesHandler(dir, nil)
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
	h := FilesHandler(filepath.Join(data, "shares"), nil)
	for _, p := range []string{"/../shares.json", "/%2e%2e/shares.json", "/site123456/../../shares.json", "/..%2fshares.json"} {
		if body := get(t, h, p).Body.String(); strings.Contains(body, "leakedid00") {
			t.Errorf("%s: served shares.json", p)
		}
	}
}

func getAuth(t *testing.T, h http.Handler, path, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetBasicAuth("anyone", password)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A protected share's files, every one of them, must be unreadable without its password, also via path
// tricks that http.FileServer would clean into the share's directory; other shares stay open.
func TestFilesRequirePasswordForProtectedShare(t *testing.T) {
	dir := setupShares(t)
	store, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	pw, hash, err := share.NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(share.Share{ID: "site123456", PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(share.Share{ID: "single1234"}); err != nil {
		t.Fatal(err)
	}
	h := FilesHandler(dir, store)

	for _, p := range []string{"/site123456/", "/site123456/assets/app.js", "/x/../site123456/assets/app.js", "//site123456/assets/app.js"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "js") {
			t.Errorf("%s without password: %d %q", p, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != cacheControl || rec.Header().Get("WWW-Authenticate") != "" || !strings.Contains(rec.Body.String(), `<form method="post">`) {
			t.Errorf("%s: 401 must be no-store and show the form, not trigger the browser dialog: %v", p, rec.Header())
		}
		if rec := getAuth(t, h, p, "wrong-pass"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with wrong password: %d", p, rec.Code)
		}
	}
	if rec := getAuth(t, h, "/site123456/assets/app.js", pw); rec.Code != http.StatusOK || rec.Body.String() != "js" {
		t.Errorf("right password: %d %q", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/single1234/report.html"); rec.Code != http.StatusOK {
		t.Errorf("unprotected share must stay open: %d", rec.Code)
	}
}

func protectedShare(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	store, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	pw, hash, err := share.NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(share.Share{ID: "site123456", PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	return FilesHandler(setupShares(t), store), pw, hash
}

func getCookie(t *testing.T, h http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postPassword(t *testing.T, h http.Handler, path, password string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{"password": {password}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The form must unlock the whole share (its frame page and assets too) for this browser only: the cookie is
// scoped to the share's path, unreadable by scripts, Secure behind Cloudflare's HTTPS, and useless for another
// share.
func TestFilesPasswordFormSetsShareCookie(t *testing.T) {
	h, pw, _ := protectedShare(t)

	if rec := postPassword(t, h, "/site123456/", "wrong-pass", nil); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "Wrong password") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("wrong password: %d, cookies %v", rec.Code, rec.Result().Cookies())
	}
	rec := postPassword(t, h, "/site123456/", " "+pw+" ", map[string]string{"X-Forwarded-Proto": "https"})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/site123456/" {
		t.Fatalf("right password: %d, location %q; want a redirect back to the page", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	c := cookies[0]
	if c.Path != "/site123456" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || strings.Contains(c.Value, pw) {
		t.Fatalf("cookie = %+v; want share path, HttpOnly, Secure, Lax, and never the password", c)
	}

	// browsers match Path=/site123456 on whole segments, so a share whose ID extends this one never gets it
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "https", Host: "h", Path: "/site123456/"}, cookies)
	for p, want := range map[string]int{"/site123456": 1, "/site123456/assets/app.js": 1, "/site1234567/": 0, "/site123456x": 0} {
		if got := len(jar.Cookies(&url.URL{Scheme: "https", Host: "h", Path: p})); got != want {
			t.Errorf("browser sends %d cookies to %s, want %d", got, p, want)
		}
	}

	if rec := getCookie(t, h, "/site123456/assets/app.js", c); rec.Code != http.StatusOK || rec.Body.String() != "js" {
		t.Fatalf("asset with cookie: %d %q", rec.Code, rec.Body)
	}
	if rec := getCookie(t, h, "/site123456/", &http.Cookie{Name: c.Name, Value: "not-the-hash"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie: %d", rec.Code)
	}
	if rec := postPassword(t, h, "/site123456/", pw, nil); rec.Result().Cookies()[0].Secure {
		t.Fatal("plain-HTTP local use must not get a Secure cookie the browser would drop")
	}
}
