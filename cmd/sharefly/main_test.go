package main

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackmajoris/sharefly/pkg/server"
	"github.com/hackmajoris/sharefly/pkg/share"
)

func captureStdout(t *testing.T, f func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	code := f()
	os.Stdout = orig
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// Scripts rely on exit codes: 0 success (including -h), 1 runtime error, 2 usage error.
func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no command", nil, 2},
		{"unknown command", []string{"bogus"}, 2},
		{"help", []string{"serve", "-h"}, 0},
		{"top-level help", []string{"--help"}, 0},
		{"config help", []string{"config", "--help"}, 0},
		{"bad config command", []string{"config", "bogus"}, 1},
		{"runtime error", []string{"rm"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.args); got != tt.want {
				t.Fatalf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

// `serve` output is meant to be piped (e.g. to pbcopy), so stdout must be exactly the URL.
func TestRunServePrintsOnlyURLAndRmIsSilent(t *testing.T) {
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
	f := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(f, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := captureStdout(t, func() int { return run([]string{"serve", f, "--server", ts.URL}) })
	shares := store.List()
	if code != 0 || len(shares) != 1 {
		t.Fatalf("serve: code %d, shares %v", code, shares)
	}
	if want := "https://share.example.com/" + shares[0].ID + "/report.html\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}

	code, out = captureStdout(t, func() int { return run([]string{"rm", shares[0].ID, "--server", ts.URL}) })
	if code != 0 || out != "" {
		t.Fatalf("rm: code %d, stdout %q; want 0 and no output", code, out)
	}
	if code := run([]string{"rm", shares[0].ID, "--server", ts.URL}); code != 1 {
		t.Fatalf("rm of unknown id: code %d, want 1", code)
	}
	if _, out := captureStdout(t, func() int { return run([]string{"ls", "--server", ts.URL}) }); !strings.HasPrefix(out, "ID") {
		t.Fatalf("ls output = %q, want table header", out)
	}
}
