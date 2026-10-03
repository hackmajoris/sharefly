package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseServerFlagsRequired(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing public-url", []string{"--api-addr", "127.0.0.1:8787"}, "--public-url"},
		{"missing api-addr", []string{"--public-url", "https://s.example.com"}, "--api-addr"},
		{"extra args", []string{"--api-addr", "x:1", "--public-url", "https://s", "stray"}, "unexpected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseServerFlags(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestParseServerFlagsDefaults(t *testing.T) {
	cfg, err := parseServerFlags([]string{"--api-addr", "100.64.0.1:8787", "--public-url", "https://s.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.publicAddr != "127.0.0.1:8080" {
		t.Errorf("public listener must default to loopback so only cloudflared reaches it, got %q", cfg.publicAddr)
	}
	if !strings.HasSuffix(cfg.dataDir, "go-share") {
		t.Errorf("dataDir = %q, want ~/go-share", cfg.dataDir)
	}
}

func TestRunServerBindFailure(t *testing.T) {
	dir := t.TempDir()
	err := runServer([]string{"--api-addr", "256.0.0.1:1", "--public-url", "https://s", "--data-dir", dir})
	if err == nil {
		t.Fatal("bind failure must return an error so the process exits non-zero and launchd retries")
	}
	for _, d := range []string{"shares", "tmp"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err != nil {
			t.Errorf("%s dir must be created before serving, since the API assumes it exists: %v", d, err)
		}
	}
}
