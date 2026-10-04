package main

import (
	"bytes"
	"encoding/json"
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
	dataDir := t.TempDir()
	var restartedFrom []string
	restart := func(old string) error { restartedFrom = append(restartedFrom, old); return nil }
	for _, kv := range [][2]string{
		{"public-url", "https://share.example.com"}, {"server", "http://host:8787"}, {"data-dir", dataDir},
		{"api-addr", "100.64.0.1:8787"}, {"public-addr", "127.0.0.1:9090"}, {"ttl", "1d"},
	} {
		if err := setConfig(kv[0], kv[1], restart); err != nil {
			t.Fatalf("set %s: %v", kv[0], err)
		}
	}
	want := config{PublicURL: "https://share.example.com", Server: "http://host:8787", DataDir: dataDir,
		APIAddr: "100.64.0.1:8787", PublicAddr: "127.0.0.1:9090", TTL: "1d"}
	if c, err := loadConfig(); err != nil || c != want {
		t.Fatalf("after set: %+v, %v; want %+v", c, err, want)
	}
	if len(restartedFrom) != 4 {
		t.Errorf("restarts = %d; every server-side key (public-url, data-dir, api-addr, public-addr) must restart, client keys must not", len(restartedFrom))
	}
	if err := setConfig("server", "", restart); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadConfig(); c.Server != "" || c.PublicURL == "" {
		t.Errorf("unset server must clear only that key, got %+v", c)
	}
}

// Moving data-dir must restart the server that is still using the old dir, or it is never found again.
func TestSetConfigDataDirRestartsFromOldDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldDir, newDir := t.TempDir(), t.TempDir()
	if err := saveConfig(config{DataDir: oldDir}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := setConfig("data-dir", newDir, func(old string) error { got = old; return nil }); err != nil {
		t.Fatal(err)
	}
	if got != oldDir {
		t.Errorf("restart looked in %q, want the old data dir %q", got, oldDir)
	}
}

func TestSetConfigRejectsBadInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	noRestart := func(string) error { t.Fatal("must not restart on invalid input"); return nil }
	if err := setConfig("bogus", "x", noRestart); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown key: err = %v", err)
	}
	bad := map[string][]string{
		"public-url":  {"share.example.com", "ftp://x", "https://"},
		"server":      {"macmini:8787"},
		"data-dir":    {"relative/dir"},
		"api-addr":    {"8787", "localhost"},
		"public-addr": {"nope"},
		"ttl":         {"7w", "0d"},
	}
	for key, values := range bad {
		for _, v := range values {
			if err := setConfig(key, v, noRestart); err == nil {
				t.Errorf("%s %q accepted, want error", key, v)
			}
		}
	}
	if c, _ := loadConfig(); c != (config{}) {
		t.Errorf("rejected input must not be saved, got %+v", c)
	}
}

// `sharefly config` must show every effective value, defaults included, so users can see where shares live.
func TestShowConfigListsEffectiveValues(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	if err := saveConfig(config{PublicURL: "https://file.example.com"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := showConfig(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"config.json",
		"https://file.example.com", "(config)",
		"/tmp/state/sharefly", "(default)",
		"127.0.0.1:8787", "127.0.0.1:8080", "7d",
	} {
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

// fakeEditor returns an "editor" that overwrites the file it is given with content.
func fakeEditor(t *testing.T, content string) []string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "content.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncp \""+filepath.Join(dir, "content.json")+"\" \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{script}
}

// Editing a server-side key by hand must reach the running server, same as `config set`.
func TestOpenConfigRestartsAfterServerSideEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	restarts := 0
	restart := func(string) error { restarts++; return nil }
	if err := openConfig(fakeEditor(t, `{"public_url": "https://share.example.com"}`), restart); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadConfig(); c.PublicURL != "https://share.example.com" {
		t.Errorf("config after edit = %+v", c)
	}
	if restarts != 1 {
		t.Errorf("restarts = %d, want 1 after changing public_url", restarts)
	}
}

func TestOpenConfigNoRestartForClientOnlyEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	restart := func(string) error { t.Fatal("ttl is client-only; must not restart the server"); return nil }
	if err := openConfig(fakeEditor(t, `{"ttl": "1h"}`), restart); err != nil {
		t.Fatal(err)
	}
}

// A typo or invalid value saved in the editor must be reported right away, not on the next share.
func TestOpenConfigReportsInvalidEdit(t *testing.T) {
	for name, content := range map[string]string{
		"syntax":      `{"public_url": `,
		"unknown key": `{"public-url": "https://share.example.com"}`,
		"bad value":   `{"api_addr": "8787"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			restart := func(string) error { t.Fatal("must not restart with a broken config"); return nil }
			if err := openConfig(fakeEditor(t, content), restart); err == nil {
				t.Fatal("want error for invalid config")
			}
		})
	}
}

func TestOpenConfigCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	editor := []string{"true"}
	if err := openConfig(editor, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sharefly", "config.json")); err != nil {
		t.Errorf("config file not created before opening the editor: %v", err)
	}
}

func TestEditorCommandPrefersVisual(t *testing.T) {
	t.Setenv("VISUAL", "code --wait")
	t.Setenv("EDITOR", "vi")
	if got := editorCommand(); strings.Join(got, " ") != "code --wait" {
		t.Errorf("editorCommand() = %v, want VISUAL split into words", got)
	}
	t.Setenv("VISUAL", "")
	if got := editorCommand(); strings.Join(got, " ") != "vi" {
		t.Errorf("editorCommand() = %v, want EDITOR", got)
	}
	t.Setenv("EDITOR", "")
	if got := editorCommand(); got != nil {
		t.Errorf("editorCommand() = %v, want nil so the GUI fallback is used", got)
	}
}

// The file must document every key with its default, so `config open` shows what can be changed.
func TestSavedConfigListsEveryKeyWithDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	if err := saveConfig(config{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sharefly", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"public_url": "", "server": "",
		"data_dir": "/tmp/state/sharefly", "api_addr": "127.0.0.1:8787", "public_addr": "127.0.0.1:8080", "ttl": "7d",
	}
	if len(raw) != len(want) {
		t.Errorf("file has %d keys, want %d: %s", len(raw), len(want), data)
	}
	for k, v := range want {
		if got, ok := raw[k]; !ok || got != v {
			t.Errorf("%s = %q (present %v), want %q", k, got, ok, v)
		}
	}
}

// Writing the derived default into the file would pin `server` to 127.0.0.1: a host that later sets api_addr
// to its tailnet IP would then talk to (and auto-start) a second, conflicting local server.
func TestDerivedKeysKeepFollowingAfterSave(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{}); err != nil {
		t.Fatal(err)
	}
	if err := setConfig("api-addr", "100.64.0.1:8787", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := setConfig("public-addr", "127.0.0.1:9090", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := c.value(serverKey); got != "http://100.64.0.1:8787" {
		t.Errorf("server = %q, want it to follow api_addr", got)
	}
	if got, _, _ := c.value(configKeys[0]); got != "http://127.0.0.1:9090" {
		t.Errorf("public_url = %q, want it to follow public_addr", got)
	}
}

// An existing `{}` must be filled in when opened, and filling in defaults is not a change that restarts the server.
func TestOpenConfigFillsExistingEmptyFileWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "sharefly", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	restart := func(string) error { t.Fatal("unchanged effective values must not restart"); return nil }
	if err := openConfig([]string{"true"}, restart); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"api_addr": "127.0.0.1:8787"`) {
		t.Errorf("file not filled with defaults:\n%s", data)
	}
}
