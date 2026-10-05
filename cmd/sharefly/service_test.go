package main

import (
	"encoding/xml"
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

// fakeService pretends a service is installed (or not) on goos and records the system commands instead of
// running them; onRun lets a test react to a command, e.g. start a server when the service manager would.
func fakeService(t *testing.T, goos string, installed bool, onRun func(argv []string)) *[][]string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sharefly.service")
	if installed {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldFile, oldOS, oldRun := serviceFile, serviceOS, runCommand
	t.Cleanup(func() { serviceFile, serviceOS, runCommand = oldFile, oldOS, oldRun })
	serviceFile, serviceOS = file, goos
	var ran [][]string
	runCommand = func(argv []string) error {
		ran = append(ran, argv)
		if onRun != nil {
			onRun(argv)
		}
		return nil
	}
	return &ran
}

func joined(cmds [][]string) string {
	var out []string
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return strings.Join(out, "\n")
}

func serveListShares(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(listShares), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// launchd and systemd start the server with no shell and a bare environment: the file must name the user, HOME
// (where the config lives), a PATH that finds cloudflared, the `server` subcommand, and restart on exit.
func TestRenderServiceCarriesWhatTheServerNeeds(t *testing.T) {
	spec := serviceSpec{User: "alex", Home: "/Users/alex", Exe: "/opt/homebrew/bin/sharefly",
		Path: "/opt/homebrew/bin:/usr/bin", LogFile: "/Users/alex/.local/state/sharefly/server.log"}

	plist := renderService("darwin", spec)
	if err := xml.Unmarshal([]byte(plist), new(struct{})); err != nil {
		t.Fatalf("plist is not valid XML: %v", err)
	}
	for _, want := range []string{"<string>alex</string>", "<string>/Users/alex</string>", "<string>/opt/homebrew/bin:/usr/bin</string>",
		"<string>/opt/homebrew/bin/sharefly</string><string>server</string>", "<key>KeepAlive</key><true/>", "<key>RunAtLoad</key><true/>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	spec.Home = "/Users/a&b"
	if p := renderService("darwin", spec); !strings.Contains(p, "/Users/a&amp;b") {
		t.Error("plist values must be XML-escaped")
	}

	spec.Home = "/home/alex"
	unit := renderService("linux", spec)
	for _, want := range []string{"User=alex", `Environment="HOME=/home/alex"`, `Environment="PATH=/opt/homebrew/bin:/usr/bin"`,
		`ExecStart="/opt/homebrew/bin/sharefly" server`, "Restart=always", "StartLimitIntervalSec=0", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q", want)
		}
	}
}

// stop must take the server out of the service manager: a plain SIGTERM would be undone by KeepAlive/Restart=always.
func TestServiceStopUsesTheServiceManager(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "sudo launchctl bootout system/" + serviceLabel, "linux": "sudo systemctl stop " + serviceUnit} {
		ran := fakeService(t, goos, true, nil)
		if err := runStop(nil); err != nil {
			t.Fatal(err)
		}
		if got := joined(*ran); got != want {
			t.Errorf("%s stop ran %q, want %q", goos, got, want)
		}
	}
}

// The service has no flags; start flags would silently not apply, and a hand-started server would fight it.
func TestStartWithServiceInstalled(t *testing.T) {
	addr := freeAddr(t)
	if err := saveConfig(config{APIAddr: addr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = saveConfig(config{}) })
	ran := fakeService(t, "linux", true, func(argv []string) {
		if strings.Join(argv, " ") == "sudo systemctl start "+serviceUnit {
			serveListShares(t, addr)
		}
	})
	if err := runStart([]string{"--tunnel", "quick"}); err == nil || !strings.Contains(err.Error(), "config set") {
		t.Fatalf("start with flags: err = %v, want a pointer to config set", err)
	}
	if err := runStart(nil); err != nil {
		t.Fatal(err)
	}
	if got := joined(*ran); got != "sudo systemctl start "+serviceUnit {
		t.Errorf("ran %q", got)
	}
}

// serve must not start a second, unsupervised server next to a stopped service.
func TestServeDoesNotAutoStartBesideTheService(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeService(t, "darwin", true, nil)
	if _, err := spawnLocalServer(freeAddr(t), ""); err == nil || !strings.Contains(err.Error(), "sharefly start") {
		t.Fatalf("err = %v, want a pointer to sharefly start", err)
	}
}

// A config change restarts the service's server by signal only; the service manager brings it back, so
// starting one here would race it for the ports.
func TestRestartServerUnderServiceSignalsOnly(t *testing.T) {
	addr := freeAddr(t)
	dataDir := t.TempDir()
	if err := saveConfig(config{APIAddr: addr, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = saveConfig(config{}) })
	ran := fakeService(t, "darwin", true, nil)
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
	serveListShares(t, addr) // the service manager's restarted server
	spawn := func([]string, string) (int, string, error) {
		t.Fatal("must not start a server by hand")
		return 0, "", nil
	}
	if err := restartServer(dataDir, spawn, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("old server was not signalled")
	}
	if len(*ran) != 0 {
		t.Errorf("restart ran service commands %v; a signal is enough", *ran)
	}
}

// install writes the rendered file through sudo, loads it, and returns once the server answers.
func TestServiceInstall(t *testing.T) {
	addr := freeAddr(t)
	dataDir := t.TempDir()
	if err := saveConfig(config{APIAddr: addr, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = saveConfig(config{}) })
	var rendered string
	ran := fakeService(t, "linux", false, nil)
	runCommand = func(argv []string) error {
		*ran = append(*ran, argv)
		switch strings.Join(argv[:2], " ") {
		case "sudo install":
			data, err := os.ReadFile(argv[4])
			if err != nil {
				return err
			}
			rendered = string(data)
		case "sudo systemctl":
			if argv[2] == "enable" {
				serveListShares(t, addr)
			}
		}
		return nil
	}
	if err := runService([]string{"install"}); err != nil {
		t.Fatal(err)
	}
	cmds := joined(*ran)
	for _, want := range []string{"sudo install -m 0644 ", " " + serviceFile, "sudo systemctl daemon-reload", "sudo systemctl enable --now " + serviceUnit} {
		if !strings.Contains(cmds, want) {
			t.Errorf("commands missing %q:\n%s", want, cmds)
		}
	}
	if !strings.Contains(rendered, "StandardOutput=append:"+filepath.Join(dataDir, logFileName)) || !strings.Contains(rendered, " server\n") {
		t.Errorf("rendered unit:\n%s", rendered)
	}
}

// Installing next to a running hand-started server, or twice, would leave two servers fighting for the ports.
func TestServiceInstallRefusals(t *testing.T) {
	fakeService(t, "darwin", true, nil)
	if err := runService([]string{"install"}); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("installed twice: %v", err)
	}

	addr := freeAddr(t)
	if err := saveConfig(config{APIAddr: addr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = saveConfig(config{}) })
	serveListShares(t, addr)
	ran := fakeService(t, "darwin", false, nil)
	if err := runService([]string{"install"}); err == nil || !strings.Contains(err.Error(), "sharefly stop") {
		t.Fatalf("server running: %v", err)
	}
	if len(*ran) != 0 {
		t.Errorf("ran %v despite refusing", *ran)
	}
	if err := runService([]string{"uninstall"}); err == nil {
		t.Error("uninstall without a service must fail")
	}
}
