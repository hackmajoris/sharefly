package server

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

const repoURL = "https://github.com/hackmajoris/sharefly"

// pageCSP applies to sharefly's own pages (frame, Open), never to the shared files.
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'; form-action 'self'; base-uri 'none'"

func openCookieName(id string) string { return "sharefly_open_" + id }

// isOpener reports whether r carries the cookie of the visitor who opened the once share sh.
func isOpener(r *http.Request, sh share.Share) bool {
	c, err := r.Cookie(openCookieName(sh.ID))
	return err == nil && sh.OpenToken != "" &&
		subtle.ConstantTimeCompare([]byte(c.Value), []byte(sh.OpenToken)) == 1
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// openGate shows an unopened once share's Open button; only its POST opens the share, so link previews (which
// only GET) never use up the view. The opener gets the cookie and is redirected to the frame page.
func openGate(w http.ResponseWriter, r *http.Request, store *share.Store, sh share.Share) {
	if r.Method != http.MethodPost {
		writePage(w, http.StatusOK, openPage, nil)
		return
	}
	token, err := share.NewToken()
	if err != nil {
		log.Printf("open %s: %v", sh.ID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := store.Open(sh.ID, token, time.Now()); err != nil {
		if errors.Is(err, share.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("open %s: %v", sh.ID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     openCookieName(sh.ID),
		Value:    token,
		Path:     "/" + sh.ID,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, r.URL.EscapedPath(), http.StatusSeeOther)
}

// framePage shows the share in an iframe between a header with its expiry and the sharefly footer. The shared
// files themselves are served unchanged at /<id>/<entry>.
func framePage(w http.ResponseWriter, sh share.Share, now time.Time) {
	title := sh.Name
	if title == "" {
		title = sh.ID
	}
	writePage(w, http.StatusOK, frameTmpl, map[string]any{
		"Title":  title,
		"Src":    (&url.URL{Path: "/" + sh.ID + "/" + sh.Entry}).EscapedPath(),
		"Expiry": expiryText(sh.ExpiresAt, now),
		"Once":   sh.Once,
		"Repo":   repoURL,
	})
}

func expiryText(exp *time.Time, now time.Time) string {
	if exp == nil {
		return "Never expires"
	}
	d := exp.Sub(now)
	var rel string
	switch {
	case d < time.Hour:
		rel = fmt.Sprintf("%d min", max(1, int(d.Round(time.Minute)/time.Minute)))
	case d < 48*time.Hour:
		rel = fmt.Sprintf("%d h", int(d.Round(time.Hour)/time.Hour))
	default:
		rel = fmt.Sprintf("%d days", int(d.Round(24*time.Hour)/(24*time.Hour)))
	}
	return "Expires in " + rel + " · " + exp.UTC().Format("2 Jan 2006 15:04 UTC")
}

func writePage(w http.ResponseWriter, code int, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.WriteHeader(code)
	if err := t.Execute(w, data); err != nil {
		log.Printf("%s page: %v", t.Name(), err)
	}
}

var openPage = template.Must(template.New("open").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>One-time link</title>
<style>
` + cardCSS + `</style>
</head>
<body>
<form method="post">
  <div class="lock" aria-hidden="true">&#128065;</div>
  <h1>One-time link</h1>
  <p>This page can be opened once. After you open it, the link stops working for everyone, including you, a minute later.</p>
  <button type="submit">Open</button>
</form>
</body>
</html>
`))

var notFoundPage = template.Must(template.New("not found").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Link not available</title>
<style>
` + cardCSS + `.by { margin: 20px 0 0; font-size: 12px; text-align: center; }
a { color: var(--accent); }
</style>
</head>
<body>
<main>
  <div class="lock" aria-hidden="true">&#8987;</div>
  <h1>Link not available</h1>
  <p>This link has expired, was already opened, or never existed. Ask the person who sent it for a new one.</p>
  <p class="by">Shared with <a href="{{.}}" target="_blank" rel="noopener">sharefly</a></p>
</main>
</body>
</html>
`))

var frameTmpl = template.Must(template.New("frame").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}}</title>
<style>
:root { --bg: #f6f7f9; --fg: #1d2129; --muted: #6b7280; --line: #dfe3e8; --accent: #3b6fd8; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #111317; --fg: #e8eaed; --muted: #9aa1ab; --line: #2c3139; --accent: #7aa2f7; }
}
* { box-sizing: border-box; }
html, body { height: 100%; }
body { margin: 0; height: 100dvh; display: flex; flex-direction: column; background: var(--bg); color: var(--fg);
  font: 13px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 4px 16px;
  padding: 8px 16px; border-bottom: 1px solid var(--line); }
.name { font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.exp { color: var(--muted); font-variant-numeric: tabular-nums; }
.once { color: var(--accent); font-weight: 600; margin-right: 6px; }
iframe { flex: 1; width: 100%; border: 0; background: #fff; }
footer { padding: 6px 16px; border-top: 1px solid var(--line); text-align: center; color: var(--muted); font-size: 12px; }
a { color: var(--accent); }
</style>
</head>
<body>
<header>
  <span class="name">{{.Title}}</span>
  <span class="exp">{{if .Once}}<span class="once">One-time view</span>{{end}}{{.Expiry}}</span>
</header>
<iframe src="{{.Src}}" title="{{.Title}}" allowfullscreen></iframe>
<footer>Shared with <a href="{{.Repo}}" target="_blank" rel="noopener">sharefly</a></footer>
</body>
</html>
`))
