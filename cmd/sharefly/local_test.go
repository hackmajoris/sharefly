package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackmajoris/sharefly/pkg/client"
)

func TestLocalAPIAddr(t *testing.T) {
	tests := []struct {
		url, want string
		ok        bool
	}{
		{"http://127.0.0.1:8787", "127.0.0.1:8787", true},
		{"http://localhost:9000/", "localhost:9000", true},
		{"http://[::1]:8787", "[::1]:8787", true},
		{"http://macmini:8787", "", false},
		{"http://100.64.0.1:8787", "", false},
		{"http://127.0.0.1", "", false},
	}
	for _, tt := range tests {
		got, ok := localAPIAddr(tt.url)
		if got != tt.want || ok != tt.ok {
			t.Errorf("localAPIAddr(%q) = %q, %v; want %q, %v", tt.url, got, ok, tt.want, tt.ok)
		}
	}
}

func listShares(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[]"))
}

// The whole point of auto-start: `serve` on a machine with no server running must just work.
func TestEnsureLocalServerStartsServerWhenNoneRuns(t *testing.T) {
	addr := freeAddr(t)
	var spawned string
	spawn := func(apiAddr, _ string) error {
		spawned = apiAddr
		ln, err := net.Listen("tcp", apiAddr)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return nil
	}
	if err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, "", spawn, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if spawned != addr {
		t.Errorf("spawned on %q, want the client's address %q", spawned, addr)
	}
}

func TestEnsureLocalServerLeavesRunningServerAlone(t *testing.T) {
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	spawn := func(string, string) error { t.Fatal("must not start a second server"); return nil }
	if err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, "", spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

// On a host whose api-addr is its tailnet IP, `serve` targets that address and must still auto-start it.
func TestOwnServerAddrMatchesConfiguredAPIAddr(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{APIAddr: "100.64.0.1:8787"}); err != nil {
		t.Fatal(err)
	}
	if addr, ok := ownServerAddr("http://100.64.0.1:8787"); !ok || addr != "100.64.0.1:8787" {
		t.Errorf("own tailnet address: got %q, %v", addr, ok)
	}
	if _, ok := ownServerAddr("http://100.64.0.2:8787"); ok {
		t.Error("another machine's address must not count as this machine's server")
	}
	if addr, ok := ownServerAddr("http://127.0.0.1:8787"); !ok || addr != "127.0.0.1:8787" {
		t.Errorf("loopback must still count, got %q, %v", addr, ok)
	}
}

// A server on another machine can't be started from here; its errors must surface unchanged.
func TestEnsureLocalServerIgnoresRemoteTargets(t *testing.T) {
	spawn := func(string, string) error { t.Fatal("must not spawn for a remote server"); return nil }
	if err := ensureLocalServer(&client.Client{BaseURL: "http://macmini:8787"}, "", spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLocalServerFailsWhenSpawnedServerNeverAnswers(t *testing.T) {
	addr := freeAddr(t)
	err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, "", func(string, string) error { return nil }, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not come up") {
		t.Fatalf("err = %v, want did not come up", err)
	}
}

// A server whose pid file is gone (older build, other data dir, deleted by hand) must not be reported as
// "not running" while it still answers; that left an unstoppable server serving stale shares.
func TestRunStopReportsServerWithoutPidFile(t *testing.T) {
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{Server: "http://" + addr}); err != nil {
		t.Fatal(err)
	}
	err = runStop([]string{"--data-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no pid file") || !strings.Contains(err.Error(), "kill") {
		t.Fatalf("err = %v, want a pointer to the running server and how to stop it", err)
	}
}

func TestRunStopNothingRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveConfig(config{Server: "http://" + freeAddr(t)}); err != nil {
		t.Fatal(err)
	}
	if err := runStop([]string{"--data-dir", t.TempDir()}); !errors.Is(err, errNotRunning) {
		t.Fatalf("err = %v, want errNotRunning", err)
	}
}

func TestStopServerNotRunning(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), pidFileName)
	if err := stopServer(pidFile, time.Second); err == nil || !strings.Contains(err.Error(), "no local sharefly server") {
		t.Fatalf("err = %v, want not running", err)
	}
}

// A crash leaves a pid file behind; stop must say so and clean it up instead of signalling a random pid forever.
func TestStopServerRemovesStalePidFile(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), pidFileName)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stopServer(pidFile, time.Second); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err = %v, want stale", err)
	}
	if pathExists(pidFile) {
		t.Error("stale pid file not removed")
	}
}

func TestStopServerTerminatesProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pidFile := filepath.Join(t.TempDir(), pidFileName)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stopServer(pidFile, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("process still running after stop")
	}
	if cmd.ProcessState == nil || cmd.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Errorf("process state %v, want terminated by SIGTERM", cmd.ProcessState)
	}
}

func TestDialAddr(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8787":    "127.0.0.1:8787",
		"[::]:8787":       "127.0.0.1:8787",
		":8787":           "127.0.0.1:8787",
		"100.64.0.1:8787": "100.64.0.1:8787",
		"127.0.0.1:8787":  "127.0.0.1:8787",
	}
	for in, want := range tests {
		if got := dialAddr(in); got != want {
			t.Errorf("dialAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// `start` must hand the user's flags to the background server unchanged, and return only once it answers.
func TestStartBackgroundSpawnsServerWithSameFlags(t *testing.T) {
	addr := freeAddr(t)
	args := []string{"--api-addr", addr, "--public-url", "https://s.example.com"}
	cfg, err := parseServerFlags(args)
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	spawn := func(a []string, dataDir string) (int, string, error) {
		gotArgs = a
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return 0, "", err
		}
		srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return 4242, filepath.Join(dataDir, logFileName), nil
	}
	if err := startBackground(cfg, args, spawn, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgs, " ") != strings.Join(args, " ") {
		t.Errorf("spawned with %v, want %v", gotArgs, args)
	}
}

// A second `start` must not launch a competing server that would only fail to bind.
func TestStartBackgroundAlreadyRunning(t *testing.T) {
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	cfg, err := parseServerFlags([]string{"--api-addr", addr})
	if err != nil {
		t.Fatal(err)
	}
	spawn := func([]string, string) (int, string, error) { t.Fatal("must not spawn"); return 0, "", nil }
	if err := startBackground(cfg, nil, spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStartBackgroundFailsWhenServerNeverAnswers(t *testing.T) {
	cfg, err := parseServerFlags([]string{"--api-addr", freeAddr(t)})
	if err != nil {
		t.Fatal(err)
	}
	spawn := func([]string, string) (int, string, error) { return 1, "server.log", nil }
	err = startBackground(cfg, nil, spawn, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not come up") {
		t.Fatalf("err = %v, want did not come up", err)
	}
}

// `serve --tunnel quick` must hand the tunnel to the server it starts, or the link it prints stays local.
func TestEnsureLocalServerPassesTunnelToSpawnedServer(t *testing.T) {
	addr := freeAddr(t)
	var gotTunnel string
	spawn := func(apiAddr, tunnel string) error {
		gotTunnel = tunnel
		ln, err := net.Listen("tcp", apiAddr)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return nil
	}
	if err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, tunnelQuick, spawn, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if gotTunnel != tunnelQuick {
		t.Errorf("spawned with tunnel %q, want %q", gotTunnel, tunnelQuick)
	}
}

// A running server without the requested tunnel would print a link that isn't public, so serve must refuse
// instead of silently ignoring --tunnel; a server already in that mode must keep working for repeated serves.
func TestEnsureLocalServerTunnelMustMatchRunningServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dataDir := filepath.Join(state, "sharefly")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	spawn := func(string, string) error { t.Fatal("must not start a second server"); return nil }
	c := &client.Client{BaseURL: "http://" + addr}

	writeArgs := func(args string) {
		if err := os.WriteFile(filepath.Join(dataDir, argsFileName), []byte(args), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeArgs(`["--api-addr","` + addr + `"]`)
	if err := ensureLocalServer(c, tunnelQuick, spawn, time.Second); err == nil || !strings.Contains(err.Error(), "sharefly stop") {
		t.Fatalf("server without tunnel: err = %v, want refusal pointing at sharefly stop", err)
	}
	writeArgs(`["--api-addr","` + addr + `","--tunnel","quick"]`)
	if err := ensureLocalServer(c, tunnelQuick, spawn, time.Second); err != nil {
		t.Fatalf("server already on quick tunnel: %v", err)
	}
}

func TestEnsureLocalServerRejectsTunnelForRemoteServer(t *testing.T) {
	spawn := func(string, string) error { t.Fatal("must not spawn for a remote server"); return nil }
	if err := ensureLocalServer(&client.Client{BaseURL: "http://macmini:8787"}, tunnelQuick, spawn, time.Second); err == nil {
		t.Fatal("--tunnel against a remote server must fail: it can't change that server's tunnel")
	}
}
