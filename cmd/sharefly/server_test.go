package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

func TestParseServerFlagsRejectsExtraArgs(t *testing.T) {
	_, err := parseServerFlags([]string{"stray"})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("err = %v, want unexpected arguments", err)
	}
}

// `sharefly start` with no flags must give a working, local-only server whose links open in a local browser.
func TestParseServerFlagsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	cfg, err := parseServerFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.apiAddr != "127.0.0.1:7787" {
		t.Errorf("api must default to loopback so nothing off-machine can manage shares, got %q", cfg.apiAddr)
	}
	if cfg.publicURL != "http://127.0.0.1:7788" {
		t.Errorf("publicURL = %q, want links pointing at the local file server", cfg.publicURL)
	}
	if cfg.publicAddr != "127.0.0.1:7788" {
		t.Errorf("public listener must default to loopback so only cloudflared reaches it, got %q", cfg.publicAddr)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "sharefly"); cfg.dataDir != want {
		t.Errorf("dataDir = %q, want %q", cfg.dataDir, want)
	}
}

// Users who relocate XDG state expect every tool to follow; a relative value is invalid per the spec and ignored.
func TestDefaultDataDirHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	if dir, err := defaultDataDir(); err != nil || dir != "/tmp/xdg-state/sharefly" {
		t.Errorf("defaultDataDir() = %q, %v; want /tmp/xdg-state/sharefly", dir, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir, _ := defaultDataDir(); dir != filepath.Join(home, ".local", "state", "sharefly") {
		t.Errorf("relative XDG_STATE_HOME must be ignored, got %q", dir)
	}
}

// An auto-started server gets no flags, so the config file is how its links become public; a flag still wins.
func TestParseServerFlagsPublicURLPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{PublicURL: "https://file.example.com"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseServerFlags(nil)
	if err != nil || cfg.publicURL != "https://file.example.com" {
		t.Errorf("config: publicURL = %q, %v", cfg.publicURL, err)
	}
	cfg, err = parseServerFlags([]string{"--public-url", "https://flag.example.com"})
	if err != nil || cfg.publicURL != "https://flag.example.com" {
		t.Errorf("flag must override config, got %q, %v", cfg.publicURL, err)
	}
	if err := saveConfig(config{}); err != nil {
		t.Fatal(err)
	}
	cfg, err = parseServerFlags([]string{"--public-addr", "127.0.0.1:9090"})
	if err != nil || cfg.publicURL != "http://127.0.0.1:9090" {
		t.Errorf("default publicURL must follow --public-addr, got %q, %v", cfg.publicURL, err)
	}
}

// A host configured once (api-addr, data-dir) must start correctly with a bare `sharefly start`.
func TestParseServerFlagsReadsConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataDir := t.TempDir()
	if err := saveConfig(config{APIAddr: "100.64.0.1:8787", PublicAddr: "127.0.0.1:9090", DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseServerFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.apiAddr != "100.64.0.1:8787" || cfg.publicAddr != "127.0.0.1:9090" || cfg.dataDir != dataDir {
		t.Errorf("cfg = %+v, want values from the config file", cfg)
	}
	if cfg, _ := parseServerFlags([]string{"--api-addr", "127.0.0.1:1"}); cfg.apiAddr != "127.0.0.1:1" {
		t.Errorf("flag must override config, got %q", cfg.apiAddr)
	}
}

// Without a home dir the default would silently become a relative path under launchd's cwd.
func TestParseServerFlagsNoHomeFailsLoud(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	base := []string{"--api-addr", "100.64.0.1:8787", "--public-url", "https://s.example.com"}
	if _, err := parseServerFlags(base); err == nil {
		t.Fatal("no HOME and no --data-dir: want error")
	}
	cfg, err := parseServerFlags(append(base, "--data-dir", "/srv/sharefly"))
	if err != nil || cfg.dataDir != "/srv/sharefly" {
		t.Fatalf("explicit --data-dir must work without HOME: %q, %v", cfg.dataDir, err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

func seedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"shares/orphan/index.html", "shares/expired/index.html", "shares/live/index.html", "tmp/partial/x"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := share.Open(filepath.Join(dir, "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for _, sh := range []share.Share{{ID: "expired", ExpiresAt: &past}, {ID: "live"}} {
		if err := store.Add(sh); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A second instance started by hand must fail to bind before touching the live instance's data:
// reconcile would otherwise empty its tmp/ and delete shares it has not recorded yet.
func TestRunServerBindFailureLeavesDataUntouched(t *testing.T) {
	dir := seedDataDir(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	err = runServer([]string{"--api-addr", freeAddr(t), "--public-addr", busy.Addr().String(),
		"--public-url", "https://s", "--data-dir", dir})
	if err == nil {
		t.Fatal("public bind failure must return an error")
	}
	for _, f := range []string{"shares/orphan", "shares/expired", "tmp/partial"} {
		if !pathExists(filepath.Join(dir, f)) {
			t.Errorf("%s removed by an instance that failed to start", f)
		}
	}
}

// Startup must reconcile and sweep before serving, and SIGTERM/SIGINT must shut down cleanly
// (nil error, so launchd sees a normal exit).
func TestRunServerStartupCleanupAndShutdown(t *testing.T) {
	dir := seedDataDir(t)
	apiAddr, publicAddr := freeAddr(t), freeAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- runServer([]string{"--api-addr", apiAddr, "--public-addr", publicAddr,
			"--public-url", "https://s", "--data-dir", dir})
	}()

	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if resp, err = http.Get("http://" + apiAddr + "/shares"); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("runServer exited early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("api never came up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var list []share.Share
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(list) != 1 || list[0].ID != "live" {
		t.Errorf("shares after startup = %+v, want only live", list)
	}
	for _, f := range []string{"shares/orphan", "shares/expired", "tmp/partial"} {
		if pathExists(filepath.Join(dir, f)) {
			t.Errorf("%s still present after startup", f)
		}
	}
	if !pathExists(filepath.Join(dir, pidFileName)) {
		t.Error("running server must write its pid file, or `sharefly stop` can't find it")
	}
	if !pathExists(filepath.Join(dir, argsFileName)) {
		t.Error("running server must record its flags, or `config set` can't restart it with them")
	}
	if resp, err := http.Get("http://" + publicAddr + "/live/"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("public listener not serving live share: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown returned %v, want nil", err)
		}
		if pathExists(filepath.Join(dir, pidFileName)) || pathExists(filepath.Join(dir, argsFileName)) {
			t.Error("pid or args file left behind after shutdown")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runServer did not shut down on SIGINT")
	}
}

// Reconcile is the only pass that removes orphaned shares; if it can't, the server must refuse to
// serve (non-zero exit, launchd retries, the log shows why) rather than keep an orphan public.
func TestRunServerRefusesToServeWhenReconcileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
	dir := seedDataDir(t)
	sharesDir := filepath.Join(dir, "shares")
	if err := os.Chmod(sharesDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sharesDir, 0o755) })
	publicAddr := freeAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- runServer([]string{"--api-addr", freeAddr(t), "--public-addr", publicAddr,
			"--public-url", "https://s", "--data-dir", dir})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runServer must return an error when reconcile fails")
		}
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		<-done
		t.Fatal("runServer kept serving with an orphan it could not remove")
	}
	if resp, err := http.Get("http://" + publicAddr + "/orphan/"); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("public listener still reachable after failed reconcile: %d", resp.StatusCode)
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

// Editing public_url by any means (set, a GUI editor, by hand) must change new links without a restart;
// a running server that kept serving 127.0.0.1 links after the user set a domain was the bug this guards.
func TestLivePublicURLFollowsConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "sharefly", "config.json")
	l := newLivePublicURL("http://127.0.0.1:7788", "127.0.0.1:7788")
	if got := l.get(); got != "http://127.0.0.1:7788" {
		t.Fatalf("initial = %q", got)
	}
	write := func(content string, mod time.Time) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write(`{"public_url": "shared.example.dev"}`, now.Add(time.Second))
	if got := l.get(); got != "https://shared.example.dev" {
		t.Errorf("after edit = %q, want the new public url", got)
	}
	write(`{"public_url": "ftp://broken"}`, now.Add(2*time.Second))
	if got := l.get(); got != "https://shared.example.dev" {
		t.Errorf("broken file must keep the last good url, got %q", got)
	}
	write(`{}`, now.Add(3*time.Second))
	if got := l.get(); got != "http://127.0.0.1:7788" {
		t.Errorf("unset public_url must fall back to the file server address, got %q", got)
	}
}

func TestPublicURLFlagPinsURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := parseServerFlags([]string{"--public-url", "https://flag.example.com"})
	if err != nil || !cfg.publicURLFlag {
		t.Fatalf("publicURLFlag = %v, %v; an explicit flag must pin the url", cfg.publicURLFlag, err)
	}
	cfg, err = parseServerFlags(nil)
	if err != nil || cfg.publicURLFlag {
		t.Fatalf("publicURLFlag = %v, %v; without the flag the url follows the config", cfg.publicURLFlag, err)
	}
}

// A token tunnel without its token or a public-url hands out links nobody can open; refuse to start instead.
func TestParseServerFlagsTokenTunnelNeedsTokenAndPublicURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	args := []string{"--tunnel", "token"}
	if _, err := parseServerFlags(args); !errors.Is(err, errNoTunnelToken) {
		t.Fatalf("no token: err = %v", err)
	}
	if err := saveConfig(config{TunnelToken: testToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseServerFlags(args); !errors.Is(err, errNoPublicURL) {
		t.Fatalf("no public-url: err = %v", err)
	}
	if _, err := parseServerFlags(append(args, "--public-url", "https://share.example.com")); err != nil {
		t.Fatalf("--public-url must satisfy token mode: %v", err)
	}
	if err := saveConfig(config{TunnelToken: testToken, PublicURL: "https://share.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseServerFlags(args); err != nil {
		t.Fatalf("config public-url must satisfy token mode: %v", err)
	}
}

// An explicit --tunnel off means "nothing public": links to a public-url nobody serves would be dead.
// A config tunnel=off keeps public-url, because a reverse proxy run outside sharefly may serve it.
func TestExplicitTunnelOffPinsLocalLinks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{PublicURL: "https://share.example.com", PublicAddr: "127.0.0.1:9090"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseServerFlags([]string{"--tunnel", "off"})
	if err != nil || publicURLFunc(cfg, nil)() != "http://127.0.0.1:9090" {
		t.Fatalf("--tunnel off: url = %q, %v; want local", publicURLFunc(cfg, nil)(), err)
	}
	cfg, err = parseServerFlags(nil)
	if err != nil || publicURLFunc(cfg, nil)() != "https://share.example.com" {
		t.Fatalf("config tunnel off: url = %q, %v; want config public-url", publicURLFunc(cfg, nil)(), err)
	}
	cfg, err = parseServerFlags([]string{"--tunnel", "off", "--public-url", "https://flag.example.com"})
	if err != nil || publicURLFunc(cfg, nil)() != "https://flag.example.com" {
		t.Fatalf("--public-url must still win: url = %q, %v", publicURLFunc(cfg, nil)(), err)
	}
}
