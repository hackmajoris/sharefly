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
	spawn := func(apiAddr string) error {
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
	if err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, spawn, 2*time.Second); err != nil {
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
	spawn := func(string) error { t.Fatal("must not start a second server"); return nil }
	if err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

// A server on another machine can't be started from here; its errors must surface unchanged.
func TestEnsureLocalServerIgnoresRemoteTargets(t *testing.T) {
	spawn := func(string) error { t.Fatal("must not spawn for a remote server"); return nil }
	if err := ensureLocalServer(&client.Client{BaseURL: "http://macmini:8787"}, spawn, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLocalServerFailsWhenSpawnedServerNeverAnswers(t *testing.T) {
	addr := freeAddr(t)
	err := ensureLocalServer(&client.Client{BaseURL: "http://" + addr}, func(string) error { return nil }, 300*time.Millisecond)
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
	t.Setenv("SHAREFLY_SERVER", "http://"+addr)
	err = runStop([]string{"--data-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no pid file") || !strings.Contains(err.Error(), "kill") {
		t.Fatalf("err = %v, want a pointer to the running server and how to stop it", err)
	}
}

func TestRunStopNothingRunning(t *testing.T) {
	t.Setenv("SHAREFLY_SERVER", "http://"+freeAddr(t))
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
