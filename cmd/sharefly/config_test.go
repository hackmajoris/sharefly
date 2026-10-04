package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Keep every test in this package away from the developer's real ~/.config/sharefly.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sharefly-config")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestConfigPathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	if p, err := configPath(); err != nil || p != "/tmp/xdg-config/sharefly/config.json" {
		t.Errorf("configPath() = %q, %v", p, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := configPath(); p != filepath.Join(home, ".config", "sharefly", "config.json") {
		t.Errorf("default configPath() = %q", p)
	}
}

func TestLoadConfigMissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := loadConfig()
	if err != nil || c != (config{}) {
		t.Fatalf("loadConfig() = %+v, %v; want empty config", c, err)
	}
}

// A corrupt file must fail loudly instead of silently falling back to localhost links.
func TestLoadConfigCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "sharefly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sharefly", "config.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("corrupt config: want error")
	}
}

func TestSetConfigRoundTripAndUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SHAREFLY_PUBLIC_URL", "")
	restarts := 0
	restart := func() error { restarts++; return nil }
	if err := setConfig("public-url", "https://share.example.com", restart); err != nil {
		t.Fatal(err)
	}
	if err := setConfig("server", "http://host:8787", restart); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig()
	if err != nil || c.PublicURL != "https://share.example.com" || c.Server != "http://host:8787" {
		t.Fatalf("after set: %+v, %v", c, err)
	}
	if restarts != 1 {
		t.Errorf("restarts = %d; public-url must restart the server so links change, server must not", restarts)
	}
	if err := setConfig("server", "", restart); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadConfig(); c.Server != "" || c.PublicURL == "" {
		t.Errorf("unset server must clear only that key, got %+v", c)
	}
}

func TestSetConfigRejectsBadInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	noRestart := func() error { t.Fatal("must not restart on invalid input"); return nil }
	if err := setConfig("api-addr", "x", noRestart); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown key: err = %v", err)
	}
	for _, bad := range []string{"share.example.com", "ftp://x", "https://", "macmini:8787"} {
		if err := setConfig("public-url", bad, noRestart); err == nil {
			t.Errorf("public-url %q accepted, want error", bad)
		}
	}
	if c, _ := loadConfig(); c != (config{}) {
		t.Errorf("rejected input must not be saved, got %+v", c)
	}
}

func TestServerFlagsUseConfigPublicURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SHAREFLY_PUBLIC_URL", "")
	if err := saveConfig(config{PublicURL: "https://file.example.com"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseServerFlags(nil)
	if err != nil || cfg.publicURL != "https://file.example.com" {
		t.Fatalf("publicURL = %q, %v; want the config file value", cfg.publicURL, err)
	}
	t.Setenv("SHAREFLY_PUBLIC_URL", "https://env.example.com")
	if cfg, _ := parseServerFlags(nil); cfg.publicURL != "https://env.example.com" {
		t.Errorf("env must override the config file, got %q", cfg.publicURL)
	}
}

func TestShowConfigReportsSources(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SHAREFLY_PUBLIC_URL", "")
	t.Setenv("SHAREFLY_SERVER", "http://env:1")
	if err := saveConfig(config{PublicURL: "https://file.example.com"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := showConfig(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"config.json", "https://file.example.com", "http://env:1 (from $SHAREFLY_SERVER"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRestartServerNoopWhenNothingRuns(t *testing.T) {
	spawn := func([]string, string) (int, string, error) { t.Fatal("must not spawn"); return 0, "", nil }
	if err := restartServer(t.TempDir(), spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

// Changing public-url must reach the running server: stop it and relaunch with the flags it had.
func TestRestartServerRelaunchesWithSameFlags(t *testing.T) {
	dataDir := t.TempDir()
	addr := freeAddr(t)
	args := []string{"--api-addr", addr, "--data-dir", dataDir}
	old := exec.Command("sleep", "30")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = old.Wait(); close(exited) }()
	t.Cleanup(func() { _ = old.Process.Kill() })
	if err := os.WriteFile(filepath.Join(dataDir, pidFileName), []byte(strconv.Itoa(old.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, argsFileName), []byte(`["--api-addr","`+addr+`","--data-dir","`+dataDir+`"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	spawn := func(a []string, _ string) (int, string, error) {
		gotArgs = a
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return 0, "", err
		}
		srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return 1, "server.log", nil
	}
	if err := restartServer(dataDir, spawn, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("old server still running")
	}
	if strings.Join(gotArgs, " ") != strings.Join(args, " ") {
		t.Errorf("relaunched with %v, want %v", gotArgs, args)
	}
}
