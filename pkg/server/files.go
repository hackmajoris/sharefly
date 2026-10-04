package server

import (
	"crypto/subtle"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/hackmajoris/sharefly/pkg/share"
)

type noListingFS struct {
	fs http.FileSystem
}

func (n noListingFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		if !isRegularFile(n.fs, path.Join(name, "index.html")) {
			_ = f.Close()
			return nil, fs.ErrNotExist
		}
	}
	return f, nil
}

func isRegularFile(fsys http.FileSystem, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular()
}

const cacheControl = "private, no-store"

type noStoreWriter struct {
	http.ResponseWriter
}

func (w noStoreWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", cacheControl)
	w.ResponseWriter.WriteHeader(code)
}

func (w noStoreWriter) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", cacheControl)
	return w.ResponseWriter.Write(b)
}

// FilesHandler serves the shares under sharesDir. A share whose record in store has a password hash shows a
// password form until the visitor has its cookie (or sends HTTP Basic auth); store may be nil when no share
// can have one.
func FilesHandler(sharesDir string, store *share.Store) http.Handler {
	files := http.FileServer(noListingFS{http.Dir(sharesDir)})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = noStoreWriter{w}
		if store != nil {
			// the same cleaning http.FileServer applies, so the ID checked is the directory it will serve
			id, _, _ := strings.Cut(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"), "/")
			if sh, err := store.Get(id); err == nil && sh.PasswordHash != "" && !authorized(r, sh) {
				passwordForm(w, r, sh)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

const maxFormBytes = 4 << 10

// authorized accepts the share's cookie or, for scripts, HTTP Basic auth. The cookie holds the stored hash:
// whoever can read shares.json can read the share's files on the same disk anyway.
func authorized(r *http.Request, sh share.Share) bool {
	if c, err := r.Cookie(cookieName(sh.ID)); err == nil &&
		subtle.ConstantTimeCompare([]byte(c.Value), []byte(sh.PasswordHash)) == 1 {
		return true
	}
	_, pw, ok := r.BasicAuth()
	return ok && share.CheckPassword(sh.PasswordHash, pw)
}

func cookieName(id string) string { return "sharefly_" + id }

// passwordForm answers 401 with the form; a POST with the right password sets the share's cookie and
// redirects back to the page with a GET.
func passwordForm(w http.ResponseWriter, r *http.Request, sh share.Share) {
	wrong := false
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if share.CheckPassword(sh.PasswordHash, strings.TrimSpace(r.PostFormValue("password"))) {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName(sh.ID),
				Value:    sh.PasswordHash,
				Path:     "/" + sh.ID + "/",
				HttpOnly: true,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, r.URL.EscapedPath(), http.StatusSeeOther)
			return
		}
		wrong = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if err := formPage.Execute(w, wrong); err != nil {
		log.Printf("password form: %v", err)
	}
}

var formPage = template.Must(template.New("form").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Password required</title>
<style>
:root { --bg: #f6f7f9; --card: #fff; --fg: #1d2129; --muted: #6b7280; --line: #dfe3e8; --accent: #3b6fd8; --bad: #c2412d; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #111317; --card: #1a1d23; --fg: #e8eaed; --muted: #9aa1ab; --line: #2c3139; --accent: #7aa2f7; --bad: #f2836b; }
}
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 16px;
  background: var(--bg); color: var(--fg); font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
form { width: 100%; max-width: 360px; padding: 32px 28px 28px; border-radius: 14px; background: var(--card);
  border: 1px solid var(--line); box-shadow: 0 12px 40px -18px rgba(0,0,0,.35); }
.lock { width: 40px; height: 40px; border-radius: 10px; display: grid; place-items: center; margin-bottom: 18px;
  background: color-mix(in srgb, var(--accent) 14%, transparent); color: var(--accent); font-size: 20px; }
h1 { margin: 0 0 6px; font-size: 19px; font-weight: 650; }
p { margin: 0 0 20px; color: var(--muted); font-size: 14px; }
input { width: 100%; padding: 11px 13px; border-radius: 9px; border: 1px solid var(--line); background: var(--bg);
  color: var(--fg); font: 15px ui-monospace, "SF Mono", Menlo, monospace; letter-spacing: .04em; }
input:focus { outline: 2px solid color-mix(in srgb, var(--accent) 45%, transparent); border-color: var(--accent); }
button { width: 100%; margin-top: 12px; padding: 11px; border: 0; border-radius: 9px; background: var(--accent);
  color: #fff; font-family: inherit; font-size: 15px; font-weight: 600; cursor: pointer; }
button:hover { filter: brightness(1.08); }
.err { margin: 10px 0 0; color: var(--bad); font-size: 13.5px; }
</style>
</head>
<body>
<form method="post">
  <div class="lock" aria-hidden="true">&#128274;</div>
  <h1>Password required</h1>
  <p>This page is protected. Enter the password you were given.</p>
  <input type="password" name="password" placeholder="xxxx-xxxx-xxxx-xxxx" autocomplete="current-password" autofocus required aria-label="Password">
  {{if .}}<div class="err" role="alert">Wrong password. Try again.</div>{{end}}
  <button type="submit">Open</button>
</form>
</body>
</html>
`))
