package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func req(t *testing.T, a *API, method, path, host, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(`{"ttl":"never"}`))
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, r)
	return rec
}

// The API has no login: a page on any website must not be able to make the visitor's browser delete or renew
// shares (cross-site request), while the CLI (no Origin) and the built-in GUI (same origin) keep working.
func TestAPIRejectsCrossOriginBrowserRequests(t *testing.T) {
	a := newTestAPI(t, 1<<20)
	id := decode[map[string]any](t, do(t, a, http.MethodPost, "/shares?name=x", tarGz(t, regular("index.html", "x"))))["id"].(string)

	for _, origin := range []string{"https://evil.example", "null", "http://127.0.0.1:9999"} {
		if rec := req(t, a, http.MethodDelete, "/shares/"+id, "127.0.0.1:8787", origin); rec.Code != http.StatusForbidden {
			t.Errorf("DELETE from %s: %d, want 403", origin, rec.Code)
		}
		if rec := req(t, a, http.MethodPost, "/shares/"+id+"/renew", "127.0.0.1:8787", origin); rec.Code != http.StatusForbidden {
			t.Errorf("renew from %s: %d, want 403", origin, rec.Code)
		}
	}
	if _, err := a.Store.Get(id); err != nil {
		t.Fatal("a cross-origin request deleted the share")
	}
	if rec := req(t, a, http.MethodPost, "/shares/"+id+"/renew", "127.0.0.1:8787", "http://127.0.0.1:8787"); rec.Code != http.StatusOK {
		t.Errorf("same-origin renew (the GUI): %d", rec.Code)
	}
	if rec := req(t, a, http.MethodDelete, "/shares/"+id, "127.0.0.1:8787", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete without Origin (the CLI): %d", rec.Code)
	}
}

// DNS rebinding points an attacker's public name at the API; its requests are then same-origin, so only the
// Host header gives them away. Even listing must fail: it would leak every share's link.
func TestAPIRejectsPublicHostNames(t *testing.T) {
	a := newTestAPI(t, 1<<20)
	for host, ok := range map[string]bool{
		"127.0.0.1:8787": true, "[::1]:8787": true, "localhost:8787": true, "100.106.188.97:8787": true,
		"hlab:8787": true, "hlab.tail7ba75c.ts.net:8787": true, "hlab.local:8787": true, "HLAB.TAIL7BA75C.TS.NET.": true,
		"evil.example:8787": false, "share.hackmajoris.dev": false, "": false,
	} {
		rec := req(t, a, http.MethodGet, "/shares", host, "")
		if (rec.Code == http.StatusOK) != ok {
			t.Errorf("Host %q: %d, want allowed=%v", host, rec.Code, ok)
		}
	}
}

// The GUI is private like the API: served on api-addr with no-store and a CSP that only lets it talk to the
// API, and never by the public file server that cloudflared exposes.
func TestUIServedOnlyByAPI(t *testing.T) {
	a := newTestAPI(t, 1<<20)
	rec := do(t, a, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>sharefly shares</title>") {
		t.Fatalf("GET / on the API: %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Header().Get("Cache-Control") != cacheControl || !strings.Contains(csp, "connect-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("headers = %v", rec.Header())
	}
	if rec := get(t, FilesHandler(setupShares(t), a.Store), "/"); strings.Contains(rec.Body.String(), "sharefly shares") {
		t.Fatal("the public file server must never serve the GUI")
	}
}
