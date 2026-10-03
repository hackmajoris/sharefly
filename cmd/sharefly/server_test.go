package main

import (
	"encoding/json"
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
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "sharefly"); cfg.dataDir != want {
		t.Errorf("dataDir = %q, want %q", cfg.dataDir, want)
	}
}

// Without a home dir the default would silently become a relative path under launchd's cwd.
func TestParseServerFlagsNoHomeFailsLoud(t *testing.T) {
	t.Setenv("HOME", "")
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
	defer ln.Close()
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
	defer busy.Close()
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
	resp.Body.Close()
	if len(list) != 1 || list[0].ID != "live" {
		t.Errorf("shares after startup = %+v, want only live", list)
	}
	for _, f := range []string{"shares/orphan", "shares/expired", "tmp/partial"} {
		if pathExists(filepath.Join(dir, f)) {
			t.Errorf("%s still present after startup", f)
		}
	}
	if resp, err := http.Get("http://" + publicAddr + "/live/"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("public listener not serving live share: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown returned %v, want nil", err)
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
	t.Cleanup(func() { os.Chmod(sharesDir, 0o755) })
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
		syscall.Kill(os.Getpid(), syscall.SIGINT)
		<-done
		t.Fatal("runServer kept serving with an orphan it could not remove")
	}
	if resp, err := http.Get("http://" + publicAddr + "/orphan/"); err == nil {
		resp.Body.Close()
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
