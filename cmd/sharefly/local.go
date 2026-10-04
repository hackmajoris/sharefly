package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hackmajoris/sharefly/pkg/client"
)

const (
	defaultAPIAddr = "127.0.0.1:8787"
	pidFileName    = "server.pid"
	logFileName    = "server.log"
	startWait      = 5 * time.Second
	pollInterval   = 100 * time.Millisecond
)

var errNotRunning = errors.New("no local sharefly server running")

// ensureLocalServer starts a background server when the client targets this machine and nothing answers.
// A remote server can't be started from here, so its connection errors are left to the caller.
func ensureLocalServer(c *client.Client, spawn func(apiAddr string) error, wait time.Duration) error {
	addr, ok := localAPIAddr(c.BaseURL)
	if !ok {
		return nil
	}
	if _, err := c.List(); !errors.Is(err, client.ErrUnreachable) {
		return nil
	}
	if err := spawn(addr); err != nil {
		return fmt.Errorf("start local server: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		_, err := c.List()
		if !errors.Is(err, client.ErrUnreachable) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("local server did not come up within %s, see %s: %w", wait, logPath(), err)
		}
		time.Sleep(pollInterval)
	}
}

func localAPIAddr(baseURL string) (string, bool) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return "", false
	}
	port := u.Port()
	if port == "" {
		return "", false
	}
	return net.JoinHostPort(u.Hostname(), port), true
}

func logPath() string {
	dir, err := defaultDataDir()
	if err != nil {
		return logFileName
	}
	return filepath.Join(dir, logFileName)
}

func spawnLocalServer(apiAddr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := defaultDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, logFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, "start", "--api-addr", apiAddr)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "started local sharefly server (pid %d, log %s)\n", cmd.Process.Pid, logf.Name())
	return cmd.Process.Release()
}

func runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory (default $XDG_STATE_HOME/sharefly or ~/.local/state/sharefly)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *dataDir == "" {
		dir, err := defaultDataDir()
		if err != nil {
			return err
		}
		*dataDir = dir
	}
	err := stopServer(filepath.Join(*dataDir, pidFileName), shutdownTimeout+time.Second)
	if !errors.Is(err, errNotRunning) {
		return err
	}
	base := resolveServer("")
	addr, ok := localAPIAddr(base)
	if !ok {
		return err
	}
	if _, listErr := (&client.Client{BaseURL: base}).List(); listErr != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(addr)
	return fmt.Errorf("a sharefly server is answering on %s but has no pid file in %s "+
		"(started by an older version or with another --data-dir); stop it with: kill $(lsof -tiTCP:%s -sTCP:LISTEN)",
		addr, *dataDir, port)
}

func stopServer(pidFile string, wait time.Duration) error {
	data, err := os.ReadFile(pidFile)
	if errors.Is(err, os.ErrNotExist) {
		return errNotRunning
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("bad pid file %s: %w", pidFile, err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			_ = os.Remove(pidFile)
			return fmt.Errorf("%w (removed stale pid file)", errNotRunning)
		}
		return err
	}
	deadline := time.Now().Add(wait)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			return fmt.Errorf("server (pid %d) did not stop within %s", pid, wait)
		}
		time.Sleep(pollInterval)
	}
	return nil
}
