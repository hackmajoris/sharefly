package server

import (
	_ "embed"
	"net/http"
)

// uiPage is the management page. It is served only by the API handler (api-addr), never by FilesHandler, so it
// is as private as the API itself: loopback or the tailnet, never the public tunnel.
//
//go:embed ui.html
var uiPage []byte

func (a *API) ui(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(uiPage)
}
