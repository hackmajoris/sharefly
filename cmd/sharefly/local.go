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
	argsFileName   = "server.args.json"
	logFileName    = "server.log"
	startWait      = 5 * time.Second
	pollInterval   = 100 * time.Millisecond
)

var errNotRunning = errors.New("no local sharefly server running")

// ensureLocalServer starts a background server when the client targets this machine and nothing answers.
// A remote server can't be started from here, so its connection errors are left to the caller.
func ensureLocalServer(c *client.Client, spawn func(apiAddr string) error, wait time.Duration) error {
	addr, ok := ownServerAddr(c.BaseURL)
	if !ok {
		return nil
	}
	if _, err := c.List(); !errors.Is(err, client.ErrUnreachable) {
		return nil
	}
	if err := spawn(addr); err != nil {
		return fmt.Errorf("start local server: %w", err)
	}
	return waitReady(c, wait, logPath())
}

func waitReady(c *client.Client, wait time.Duration, logFile string) error {
	deadline := time.Now().Add(wait)
	for {
		_, err := c.List()
		if !errors.Is(err, client.ErrUnreachable) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server did not come up within %s, see %s: %w", wait, logFile, err)
		}
		time.Sleep(pollInterval)
	}
}

func runStart(args []string) error {
	cfg, err := parseServerFlags(args)
	if err != nil {
		return err
	}
	return startBackground(cfg, args, spawnServer, startWait)
}

// startBackground launches `sharefly server` with the same flags as a detached process and returns once it answers.
func startBackground(cfg serverConfig, args []string, spawn func(args []string, dataDir string) (int, string, error), wait time.Duration) error {
	c := &client.Client{BaseURL: "http://" + dialAddr(cfg.apiAddr)}
	if _, err := c.List(); !errors.Is(err, client.ErrUnreachable) {
		fmt.Printf("sharefly server already running on %s\n", cfg.apiAddr)
		return nil
	}
	pid, logFile, err := spawn(args, cfg.dataDir)
	if err != nil {
		return err
	}
	if err := waitReady(c, wait, logFile); err != nil {
		return err
	}
	fmt.Printf("sharefly server running (pid %d, api %s, links %s, log %s)\n", pid, cfg.apiAddr, cfg.publicURL, logFile)
	return nil
}

// dialAddr turns a wildcard listen address into one a client on this machine can connect to.
func dialAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
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

// ownServerAddr reports whether baseURL points at a server this machine runs: a loopback address, or the
// configured api-addr (e.g. its tailnet IP). It returns the address that server should listen on.
func ownServerAddr(baseURL string) (string, bool) {
	if c, err := loadConfig(); err == nil {
		if apiAddr, err := c.resolve("", apiAddrKey); err == nil {
			if u, err := url.Parse(baseURL); err == nil && u.Port() != "" &&
				net.JoinHostPort(u.Hostname(), u.Port()) == dialAddr(apiAddr) {
				return apiAddr, true
			}
		}
	}
	return localAPIAddr(baseURL)
}

func configuredDataDir() (string, error) {
	c, err := loadConfig()
	if err != nil {
		return "", err
	}
	return c.resolve("", dataDirKey)
}

func logPath() string {
	dir, err := configuredDataDir()
	if err != nil {
		return logFileName
	}
	return filepath.Join(dir, logFileName)
}

func spawnLocalServer(apiAddr string) error {
	dir, err := configuredDataDir()
	if err != nil {
		return err
	}
	pid, logFile, err := spawnServer([]string{"--api-addr", apiAddr}, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "started local sharefly server (pid %d, log %s)\n", pid, logFile)
	return nil
}

// spawnServer runs `sharefly server <args>` detached from the terminal, logging to <dataDir>/server.log.
func spawnServer(args []string, dataDir string) (int, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return 0, "", err
	}
	logf, err := os.OpenFile(filepath.Join(dataDir, logFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, append([]string{"server"}, args...)...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	return pid, logf.Name(), cmd.Process.Release()
}

func runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory (default: config `data-dir`, else $XDG_STATE_HOME/sharefly or ~/.local/state/sharefly)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *dataDir, err = cfg.resolve(*dataDir, dataDirKey); err != nil {
		return err
	}
	err = stopServer(filepath.Join(*dataDir, pidFileName), shutdownTimeout+time.Second)
	if !errors.Is(err, errNotRunning) {
		return err
	}
	base, resolveErr := resolveServer("")
	if resolveErr != nil {
		return resolveErr
	}
	addr, ok := ownServerAddr(base)
	if !ok {
		return err
	}
	addr = dialAddr(addr)
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
