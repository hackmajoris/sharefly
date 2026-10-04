package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackmajoris/sharefly/pkg/client"
)

const quickURL = "https://seasonal-deck-organisms-sf.trycloudflare.com"

// fakeCloudflared puts a cloudflared script first on PATH. It records its args, whether TUNNEL_TOKEN was set,
// and its pid into dir, prints a quick tunnel banner and stays up until signalled (or exits at once if crash).
func fakeCloudflared(t *testing.T, crash bool) string {
	t.Helper()
	dir := t.TempDir()
	tail := "exec sleep 30"
	if crash {
		tail = "exit 1"
	}
	script := `#!/bin/sh
echo "$@" > "` + dir + `/args"
echo "${TUNNEL_TOKEN:-none}" > "` + dir + `/token"
echo $$ >> "` + dir + `/pids"
echo "INF Requesting new quick Tunnel on trycloudflare.com..." >&2
echo "INF |  ` + quickURL + `  |" >&2
` + tail + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cloudflared"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestNewTunnelOffAndErrors(t *testing.T) {
	if tun, err := newTunnel(tunnelOff, "", "127.0.0.1:8080"); tun != nil || err != nil {
		t.Errorf("off: tunnel %v, err %v; want nothing to run", tun, err)
	}
	if _, err := newTunnel(tunnelToken, "", "127.0.0.1:8080"); err == nil || !strings.Contains(err.Error(), "tunnel-token") {
		t.Errorf("token mode without token: err = %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := newTunnel(tunnelQuick, "", "127.0.0.1:8080"); err == nil || !strings.Contains(err.Error(), "brew install cloudflared") {
		t.Errorf("missing cloudflared: err = %v", err)
	}
}

// A quick tunnel must publish the trycloudflare URL it prints, point cloudflared at the file server,
// and stop cloudflared when the server shuts down.
func TestQuickTunnelReportsURLAndStops(t *testing.T) {
	dir := fakeCloudflared(t, false)
	tun, err := newTunnel(tunnelQuick, "", "0.0.0.0:8080")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.run(ctx); close(done) }()
	if !tun.waitURL(5 * time.Second) {
		t.Fatal("no quick tunnel URL")
	}
	if tun.URL() != quickURL {
		t.Errorf("URL = %q, want %q", tun.URL(), quickURL)
	}
	if args := readFile(t, filepath.Join(dir, "args")); args != "tunnel --no-autoupdate --url http://127.0.0.1:8080" {
		t.Errorf("args = %q", args)
	}
	pid, err := strconv.Atoi(readFile(t, filepath.Join(dir, "pids")))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel did not stop")
	}
	if alive(pid) {
		t.Error("cloudflared still running after shutdown")
	}
}

// The token must reach cloudflared only through the environment, never the command line visible in ps.
func TestTokenTunnelPassesTokenViaEnv(t *testing.T) {
	dir := fakeCloudflared(t, false)
	tun, err := newTunnel(tunnelToken, testToken, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !pathExists(filepath.Join(dir, "token")) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if got := readFile(t, filepath.Join(dir, "token")); got != testToken {
		t.Errorf("TUNNEL_TOKEN = %q, want the configured token", got)
	}
	if args := readFile(t, filepath.Join(dir, "args")); strings.Contains(args, testToken) || args != "tunnel --no-autoupdate run" {
		t.Errorf("args = %q; the token must not be on the command line", args)
	}
	if tun.URL() != "" {
		t.Error("a named tunnel must not override links with a trycloudflare URL")
	}
}

// If cloudflared dies, links stay dead until it is back, so the server must keep restarting it.
func TestTunnelRestartsAfterCrash(t *testing.T) {
	dir := fakeCloudflared(t, true)
	tun, err := newTunnel(tunnelQuick, "", "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	tun.minBackoff = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	tun.run(ctx)
	if runs := len(strings.Fields(readFile(t, filepath.Join(dir, "pids")))); runs < 3 {
		t.Errorf("cloudflared started %d times in 500ms, want repeated restarts", runs)
	}
}

// End to end: a server started with --tunnel quick hands out trycloudflare links and takes cloudflared down with it.
func TestRunServerWithQuickTunnel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := fakeCloudflared(t, false)
	dataDir := t.TempDir()
	apiAddr := freeAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- runServer([]string{"--api-addr", apiAddr, "--public-addr", freeAddr(t), "--data-dir", dataDir, "--tunnel", "quick"})
	}()
	c := clientFor(apiAddr)
	var got string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if u, err := c.PublicURL(); err == nil {
			got = u
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != quickURL {
		t.Errorf("status public_url = %q, want the quick tunnel URL", got)
	}
	pid, err := strconv.Atoi(readFile(t, filepath.Join(dir, "pids")))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServer: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not stop")
	}
	if alive(pid) {
		t.Error("cloudflared outlived the server")
	}
}

func clientFor(apiAddr string) *client.Client { return &client.Client{BaseURL: "http://" + apiAddr} }
