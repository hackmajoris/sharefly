package client

import (
	"net"
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

func TestClientUploadArchiveErrorWins(t *testing.T) {
	c, dataDir := newTestServer(t)
	site := writeTree(t, map[string]string{"a.html": "a", "b.html": "b"})
	_, err := c.Upload(site, "7d")
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("err = %v, want local index.html error rather than a server error", err)
	}
	if names, _ := os.ReadDir(filepath.Join(dataDir, "shares")); len(names) != 0 {
		t.Fatalf("nothing should be shared: %v", names)
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
	for _, err := range []error{upErr, listErr, c.Delete("x")} {
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
	f := filepath.Join(t.TempDir(), "x.html")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(f, "7w"); err == nil || strings.Contains(err.Error(), "can't reach") {
		t.Fatalf("bad ttl must surface the server's 400 message, got %v", err)
	}
}
