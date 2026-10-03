package client

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackmajoris/go-share/pkg/server"
	"github.com/hackmajoris/go-share/pkg/share"
)

func newTestServer(t *testing.T) (*Client, string) {
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
	api := &server.API{Store: store, DataDir: dir, PublicURL: "https://share.example.com", MaxBytes: 1 << 20}
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	return &Client{BaseURL: ts.URL}, dir
}

func TestClientRoundTrip(t *testing.T) {
	c, dataDir := newTestServer(t)
	site := writeTree(t, map[string]string{"index.html": "hi", "app.js": "x"})

	up, err := c.Upload(site, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != filepath.Base(site) || up.URL != "https://share.example.com/"+up.ID+"/" {
		t.Fatalf("unexpected upload result %+v", up)
	}
	if got, err := os.ReadFile(filepath.Join(dataDir, "shares", up.ID, "app.js")); err != nil || string(got) != "x" {
		t.Fatalf("streamed archive not extracted on server: %q, %v", got, err)
	}

	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != up.ID || list[0].URL != up.URL {
		t.Fatalf("list = %+v", list)
	}

	renewed, err := c.Renew(up.ID, "never")
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ExpiresAt != nil {
		t.Fatalf("renew never must clear expires_at, got %v", renewed.ExpiresAt)
	}

	if err := c.Delete(up.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := c.List(); err != nil || len(list) != 0 {
		t.Fatalf("after delete list = %+v, %v", list, err)
	}
}

func TestClientUploadSingleFileURLPointsAtFile(t *testing.T) {
	c, _ := newTestServer(t)
	f := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(f, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	up, err := c.Upload(f, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(up.URL, "/"+up.ID+"/report.html") {
		t.Fatalf("url must open the file directly, got %q", up.URL)
	}
	if up.ExpiresAt == nil || up.ExpiresAt.Sub(up.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("ttl not forwarded: %+v", up)
	}
}

// connCounter returns a client for a raw listener and a func reporting how many connections it
// accepted before a probe dialed after the call under test; accepts arrive in order, so the
// count is exact without sleeping.
func connCounter(t *testing.T) (*Client, func() int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted := make(chan string, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- conn.RemoteAddr().String()
			conn.Close()
		}
	}()
	count := func() int {
		probe, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		probe.Close()
		n := 0
		for {
			select {
			case addr := <-accepted:
				if addr == probe.LocalAddr().String() {
					return n
				}
				n++
			case <-time.After(5 * time.Second):
				t.Fatal("probe connection never accepted")
			}
		}
	}
	return &Client{BaseURL: "http://" + ln.Addr().String()}, count
}

func TestClientUploadNoIndexFailsBeforeNetwork(t *testing.T) {
	c, count := connCounter(t)
	site := writeTree(t, map[string]string{"a.html": "a", "b.html": "b"})
	_, err := c.Upload(site, "7d")
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("err = %v, want local index.html error rather than a server error", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("folder without index.html must fail before any network call, server saw %d connection(s)", n)
	}
}

// A bad --ttl must fail locally: a server 400 sent before the body is read can surface as a broken pipe.
func TestClientUploadBadTTLFailsBeforeNetwork(t *testing.T) {
	c, count := connCounter(t)
	f := filepath.Join(t.TempDir(), "x.html")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(f, "7w"); err == nil || !strings.Contains(err.Error(), "invalid ttl") {
		t.Fatalf("err = %v, want invalid ttl error", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("bad ttl must fail before any network call, server saw %d connection(s)", n)
	}
}

// `serve .` must record the folder's real name, not ".".
func TestClientUploadDotUsesFolderName(t *testing.T) {
	c, _ := newTestServer(t)
	site := writeTree(t, map[string]string{"index.html": "hi"})
	t.Chdir(site)
	up, err := c.Upload(".", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != filepath.Base(site) {
		t.Fatalf("name = %q, want %q", up.Name, filepath.Base(site))
	}
}

// The server cuts an oversized upload short; the user must see the 413, not a connection error.
func TestClientUploadTooLargeSurfacesServerError(t *testing.T) {
	c, _ := newTestServer(t)
	f := filepath.Join(t.TempDir(), "big.bin")
	b := make([]byte, 4<<20)
	rand.Read(b)
	if err := os.WriteFile(f, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(f, "7d"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want server's upload too large message", err)
	}
}

// A file that can't be read mid-stream must report the local error, not whatever the server said
// about the truncated archive.
func TestClientUploadArchiveErrorWins(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	c, _ := newTestServer(t)
	site := writeTree(t, map[string]string{"index.html": "hi", "locked.js": "x"})
	if err := os.Chmod(filepath.Join(site, "locked.js"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(site, "7d"); err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want local permission error", err)
	}
}

func TestClientUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	ln.Close()

	c := &Client{BaseURL: addr}
	f := filepath.Join(t.TempDir(), "x.html")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, upErr := c.Upload(f, "7d")
	_, listErr := c.List()
	_, renewErr := c.Renew("x", "7d")
	for _, err := range []error{upErr, listErr, renewErr, c.Delete("x")} {
		if err == nil || !strings.Contains(err.Error(), "can't reach") || !strings.Contains(err.Error(), addr) {
			t.Errorf("err = %v, want actionable can't reach message naming %s", err, addr)
		}
	}
}

func TestClientSurfacesServerError(t *testing.T) {
	c, _ := newTestServer(t)
	if err := c.Delete("nope"); err == nil || !strings.Contains(err.Error(), share.ErrNotFound.Error()) {
		t.Fatalf("delete unknown: err = %v, want server message %q", err, share.ErrNotFound)
	}
	if _, err := c.Renew("nope", "7d"); err == nil || !strings.Contains(err.Error(), share.ErrNotFound.Error()) {
		t.Fatalf("renew unknown: err = %v", err)
	}
}

// Errors from a proxy or a broken server have no JSON body; the status must still reach the user.
func TestClientNonJSONResponses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Write([]byte("not json"))
	}))
	t.Cleanup(ts.Close)
	c := &Client{BaseURL: ts.URL}
	if err := c.Delete("x"); err == nil || !strings.Contains(err.Error(), "server returned 502") {
		t.Fatalf("delete: err = %v, want status fallback", err)
	}
	if _, err := c.List(); err == nil {
		t.Fatal("list: malformed success body must be an error")
	}
}

func TestClientUploadMissingPath(t *testing.T) {
	c, count := connCounter(t)
	if _, err := c.Upload(filepath.Join(t.TempDir(), "nope"), "7d"); err == nil {
		t.Fatal("want error for nonexistent path")
	}
	if n := count(); n != 0 {
		t.Fatalf("missing path must fail before any network call, server saw %d connection(s)", n)
	}
}
