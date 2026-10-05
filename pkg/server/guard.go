package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// guardBrowser keeps web pages from driving the API through a visitor's browser. The API has no login, so
// without this any site the user opens could POST to 127.0.0.1:7787 (cross-site request) or rebind its own
// name to it (DNS rebinding) and upload, renew or delete shares. The CLI sends no Origin and dials by IP or
// local name, so it passes; the built-in GUI is same-origin.
func guardBrowser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			writeError(w, http.StatusForbidden, "host not allowed: use the server's IP, localhost, or its Tailscale or .local name")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// localHost accepts the names a private API is reached by: IP literals, localhost, single-label names (hlab),
// and Tailscale (*.ts.net) or mDNS (*.local) names. A public domain name is what a rebinding attack needs.
func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return true
	}
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".ts.net") || strings.HasSuffix(host, ".local")
}
