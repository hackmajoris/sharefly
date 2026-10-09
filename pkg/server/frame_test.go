package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

func storeWith(t *testing.T, shares ...share.Share) *share.Store {
	t.Helper()
	store, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range shares {
		if err := store.Add(sh); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func request(t *testing.T, h http.Handler, method, path string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The link a visitor gets opens the frame page: expiry header, the share in an iframe, the sharefly footer.
// The share's own files must keep being served unchanged at /<id>/..., or the frame would have nothing to show.
func TestFramePageWrapsShare(t *testing.T) {
	exp := time.Now().Add(3 * time.Hour)
	h := FilesHandler(setupShares(t), storeWith(t,
		share.Share{ID: "single1234", Name: `<b>"report"</b>`, Entry: "report.html", ExpiresAt: &exp},
		share.Share{ID: "site123456"}))

	rec := get(t, h, "/single1234")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<iframe src="/single1234/report.html"`) ||
		!strings.Contains(body, "Expires in 3 h") || !strings.Contains(body, `href="`+repoURL+`"`) {
		t.Fatalf("frame page: %d %s", rec.Code, body)
	}
	if strings.Contains(body, `<b>"report"</b>`) {
		t.Error("share name must be escaped: it comes from the uploader")
	}
	if rec.Header().Get("Cache-Control") != cacheControl || rec.Header().Get("Content-Security-Policy") != pageCSP {
		t.Errorf("headers = %v; want no-store and the page CSP", rec.Header())
	}
	if body := get(t, h, "/site123456").Body.String(); !strings.Contains(body, `<iframe src="/site123456/"`) || !strings.Contains(body, "Never expires") {
		t.Errorf("folder frame page: %s", body)
	}
	for p, want := range map[string]string{"/single1234/report.html": "report", "/site123456/": "site index"} {
		if rec := get(t, h, p); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s: %d %q, want the file unchanged", p, rec.Code, rec.Body)
		}
	}
}

// A protected share's frame page shows its name and expiry, so it needs the password like the files.
func TestFramePageRequiresPassword(t *testing.T) {
	h, pw, _ := protectedShare(t)
	if rec := get(t, h, "/site123456"); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "iframe") {
		t.Fatalf("frame without password: %d %s", rec.Code, rec.Body)
	}
	if rec := getAuth(t, h, "/site123456", pw); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "iframe") {
		t.Fatalf("frame with password: %d", rec.Code)
	}
}

// Sweep runs every few minutes; an expired share must stop being served the moment it expires, which is also
// what makes a once share's grace minute a minute.
func TestFilesExpiredShareIsGone(t *testing.T) {
	past := time.Now().Add(-time.Second)
	h := FilesHandler(setupShares(t), storeWith(t, share.Share{ID: "site123456", ExpiresAt: &past}))
	for _, p := range []string{"/site123456", "/site123456/", "/site123456/assets/app.js"} {
		if rec := get(t, h, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}
}

func onceShare(t *testing.T) (http.Handler, *share.Store) {
	t.Helper()
	store := storeWith(t, share.Share{ID: "site123456", Once: true})
	return FilesHandler(setupShares(t), store), store
}

// Chat apps GET pasted links to build previews; if a GET opened a once share, the recipient would never see
// it. Nothing of the share is reachable before the visitor presses Open.
func TestOnceShareGETNeverOpens(t *testing.T) {
	h, store := onceShare(t)
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		if rec := request(t, h, m, "/site123456", nil); rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s open page: %d, cookies %v", m, rec.Code, rec.Result().Cookies())
		}
	}
	if body := get(t, h, "/site123456").Body.String(); !strings.Contains(body, `<form method="post">`) || strings.Contains(body, "iframe") {
		t.Fatalf("open page must offer the button, not the share: %s", body)
	}
	// before open the stored token is empty, so an empty cookie must not count as the opener's
	empty := &http.Cookie{Name: openCookieName("site123456"), Value: ""}
	for _, p := range []string{"/site123456/", "/site123456/assets/app.js", "/x/../site123456/assets/app.js"} {
		for _, c := range []*http.Cookie{nil, empty} {
			if rec := request(t, h, http.MethodGet, p, c); rec.Code != http.StatusNotFound {
				t.Errorf("%s before open, cookie %v: %d, want 404", p, c, rec.Code)
			}
		}
	}
	if sh, _ := store.Get("site123456"); sh.OpenToken != "" || sh.ExpiresAt != nil {
		t.Fatalf("GETs opened the share: %+v", sh)
	}
}

// Pressing Open gives that visitor alone the share, for one minute: the cookie unlocks the frame and every
// asset, a forwarded link or a second Open gets nothing.
func TestOnceShareOpensForOneVisitor(t *testing.T) {
	h, store := onceShare(t)
	req := httptest.NewRequest(http.MethodPost, "/site123456", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/site123456" {
		t.Fatalf("open: %d, location %q; want a redirect to the frame page", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	c := cookies[0]
	if c.Path != "/site123456" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v; want share path, HttpOnly, Secure, Lax", c)
	}
	sh, _ := store.Get("site123456")
	if sh.ExpiresAt == nil || time.Until(*sh.ExpiresAt) > share.OpenGrace {
		t.Fatalf("expires_at = %v; want within the grace minute", sh.ExpiresAt)
	}

	if rec := request(t, h, http.MethodGet, "/site123456", c); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "One-time view") {
		t.Fatalf("frame for opener: %d %s", rec.Code, rec.Body)
	}
	if rec := request(t, h, http.MethodGet, "/site123456/assets/app.js", c); rec.Code != http.StatusOK || rec.Body.String() != "js" {
		t.Fatalf("asset for opener: %d %q", rec.Code, rec.Body)
	}
	forged := &http.Cookie{Name: c.Name, Value: strings.Repeat("0", len(c.Value))}
	for _, ck := range []*http.Cookie{nil, forged} {
		for _, p := range []string{"/site123456", "/site123456/", "/site123456/assets/app.js"} {
			if rec := request(t, h, http.MethodGet, p, ck); rec.Code != http.StatusNotFound {
				t.Errorf("%s with cookie %v after open: %d, want 404", p, ck, rec.Code)
			}
		}
	}
	if rec := request(t, h, http.MethodPost, "/site123456", nil); rec.Code != http.StatusNotFound || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("second open: %d, cookies %v; want 404 and no cookie", rec.Code, rec.Result().Cookies())
	}
}

// A protected once share asks for the password first; submitting it must not use up the view.
func TestOncePasswordShareNeedsPasswordThenOpen(t *testing.T) {
	pw, hash, err := share.NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	store := storeWith(t, share.Share{ID: "site123456", Once: true, PasswordHash: hash})
	h := FilesHandler(setupShares(t), store)
	if rec := request(t, h, http.MethodPost, "/site123456", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("open without password: %d, want 401", rec.Code)
	}
	rec := postPassword(t, h, "/site123456", pw, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("password: %d", rec.Code)
	}
	if sh, _ := store.Get("site123456"); sh.OpenToken != "" {
		t.Fatal("the password form opened the share")
	}
	if body := getCookie(t, h, "/site123456", rec.Result().Cookies()[0]).Body.String(); !strings.Contains(body, "One-time link") {
		t.Fatalf("after password: %s; want the Open page", body)
	}
}

func TestExpiryText(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	cases := []struct {
		exp  *time.Time
		want string
	}{
		{nil, "Never expires"},
		{at(20 * time.Second), "Expires in 1 min · 9 Oct 2026 12:00 UTC"},
		{at(3 * time.Hour), "Expires in 3 h · 9 Oct 2026 15:00 UTC"},
		{at(7 * 24 * time.Hour), "Expires in 7 days · 16 Oct 2026 12:00 UTC"},
	}
	for _, c := range cases {
		if got := expiryText(c.exp, now); got != c.want {
			t.Errorf("expiryText(%v) = %q, want %q", c.exp, got, c.want)
		}
	}
}

// Every dead link, whichever way it died (unknown, expired, once already opened, missing file), shows the same
// sharefly page: a visitor learns nothing about which, and still gets no-store so the edge never caches it.
func TestNotFoundIsOnePage(t *testing.T) {
	past := time.Now().Add(-time.Second)
	store := storeWith(t, share.Share{ID: "site123456", ExpiresAt: &past}, share.Share{ID: "single1234", Once: true, OpenToken: "tok"})
	h := FilesHandler(setupShares(t), store)
	var first string
	for _, p := range []string{"/nosuchid00", "/site123456", "/single1234", "/single1234/report.html", "/nosuchid00/x.css"} {
		rec := get(t, h, p)
		body := rec.Body.String()
		if rec.Code != http.StatusNotFound || !strings.Contains(body, "Link not available") || strings.Contains(body, "404 page not found") {
			t.Errorf("%s: %d %q", p, rec.Code, body)
		}
		if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != cacheControl {
			t.Errorf("%s: headers %v", p, rec.Header())
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Errorf("%s: 404 page differs from the others", p)
		}
	}
}

// Only pages are rendered in the frame; any other file (archives, PDFs, images) gets a download button, because
// an iframe of a file the browser can't show leaves the visitor an empty frame. The button's target must be the
// file itself, served unchanged.
func TestFramePageRendersPagesDownloadsTheRest(t *testing.T) {
	dir := setupShares(t)
	writeFile(t, filepath.Join(dir, "zip1234567", "data v2.zip"), "PK")
	writeFile(t, filepath.Join(dir, "upper12345", "REPORT.HTM"), "page")
	h := FilesHandler(dir, storeWith(t,
		share.Share{ID: "zip1234567", Name: "data v2.zip", Entry: "data v2.zip", Size: 2},
		share.Share{ID: "upper12345", Name: "REPORT.HTM", Entry: "REPORT.HTM"},
		share.Share{ID: "site123456"},
		share.Share{ID: "single1234", Name: "notes.md", Entry: "report.html"}))

	body := get(t, h, "/zip1234567").Body.String()
	if strings.Contains(body, "<iframe") || !strings.Contains(body, `href="/zip1234567/data%20v2.zip" download="data v2.zip"`) ||
		!strings.Contains(body, "2 B") {
		t.Fatalf("non-page share must offer a download, not a frame: %s", body)
	}
	if rec := get(t, h, "/zip1234567/data%20v2.zip"); rec.Code != http.StatusOK || rec.Body.String() != "PK" {
		t.Fatalf("download target: %d %q", rec.Code, rec.Body)
	}
	for _, id := range []string{"upper12345", "site123456", "single1234"} {
		if body := get(t, h, "/"+id).Body.String(); !strings.Contains(body, "<iframe") || strings.Contains(body, "download=") {
			t.Errorf("%s: page share must render in the frame: %s", id, body)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
