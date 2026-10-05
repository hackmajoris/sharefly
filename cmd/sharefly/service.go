package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/hackmajoris/sharefly/pkg/client"
)

// The service starts `sharefly server` at boot as the installing user: a launchd daemon on macOS, a systemd
// unit on Linux. It runs with no flags, so the config file is its only source of settings.
const (
	serviceLabel = "dev.hackmajoris.sharefly"
	serviceUnit  = "sharefly.service"
)

var (
	serviceOS   = runtime.GOOS
	serviceFile = defaultServiceFile(runtime.GOOS)
	// runCommand runs a system command with the terminal attached, so sudo can ask for a password.
	runCommand = func(argv []string) error {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
		return nil
	}
)

func defaultServiceFile(goos string) string {
	switch goos {
	case "darwin":
		return "/Library/LaunchDaemons/" + serviceLabel + ".plist"
	case "linux":
		return "/etc/systemd/system/" + serviceUnit
	}
	return ""
}

func serviceInstalled() bool {
	if serviceFile == "" {
		return false
	}
	_, err := os.Stat(serviceFile)
	return err == nil
}

type serviceSpec struct {
	User, Home, Exe, Path, LogFile string
}

func renderService(goos string, s serviceSpec) string {
	if goos == "darwin" {
		esc := func(v string) string {
			var b bytes.Buffer
			_ = xml.EscapeText(&b, []byte(v))
			return b.String()
		}
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + serviceLabel + `</string>
  <key>UserName</key><string>` + esc(s.User) + `</string>
  <key>ProgramArguments</key>
  <array><string>` + esc(s.Exe) + `</string><string>server</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>` + esc(s.Home) + `</string>
    <key>PATH</key><string>` + esc(s.Path) + `</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>` + esc(s.LogFile) + `</string>
  <key>StandardErrorPath</key><string>` + esc(s.LogFile) + `</string>
</dict>
</plist>
`
	}
	return `[Unit]
Description=sharefly share server
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
User=` + s.User + `
Environment="HOME=` + s.Home + `"
Environment="PATH=` + s.Path + `"
ExecStart="` + s.Exe + `" server
Restart=always
RestartSec=10
StandardOutput=append:` + s.LogFile + `
StandardError=append:` + s.LogFile + `

[Install]
WantedBy=multi-user.target
`
}

// serviceCommands returns the system commands for an action; tmp is the rendered file for install.
func serviceCommands(goos, action, tmp string) [][]string {
	if goos == "darwin" {
		target := "system/" + serviceLabel
		switch action {
		case "install":
			return [][]string{{"sudo", "install", "-m", "0644", tmp, serviceFile}, {"sudo", "launchctl", "bootstrap", "system", serviceFile}}
		case "uninstall":
			return [][]string{{"sudo", "launchctl", "bootout", target}, {"sudo", "rm", "-f", serviceFile}}
		case "start":
			return [][]string{{"sudo", "launchctl", "bootstrap", "system", serviceFile}}
		case "stop":
			return [][]string{{"sudo", "launchctl", "bootout", target}}
		case "status":
			return [][]string{{"launchctl", "print", target}}
		}
		return nil
	}
	switch action {
	case "install":
		return [][]string{{"sudo", "install", "-m", "0644", tmp, serviceFile}, {"sudo", "systemctl", "daemon-reload"}, {"sudo", "systemctl", "enable", "--now", serviceUnit}}
	case "uninstall":
		return [][]string{{"sudo", "systemctl", "disable", "--now", serviceUnit}, {"sudo", "rm", "-f", serviceFile}, {"sudo", "systemctl", "daemon-reload"}}
	case "start":
		return [][]string{{"sudo", "systemctl", "start", serviceUnit}}
	case "stop":
		return [][]string{{"sudo", "systemctl", "stop", serviceUnit}}
	case "status":
		return [][]string{{"systemctl", "status", serviceUnit, "--no-pager"}}
	}
	return nil
}

func runServiceAction(action, tmp string) error {
	for _, argv := range serviceCommands(serviceOS, action, tmp) {
		if err := runCommand(argv); err != nil {
			return err
		}
	}
	return nil
}

const serviceUsage = `usage: sharefly service install | uninstall | status

  install     run the server at boot as this user (launchd on macOS, systemd on Linux); asks for sudo
  uninstall   stop the service and remove it; asks for sudo
  status      show whether the service is loaded and running

With the service installed, 'sharefly stop' and 'sharefly start' stop and start it, and changing a
server setting with 'sharefly config set' restarts it.
`

func runService(args []string) error {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, serviceUsage)
		return errors.New("service: expected install, uninstall or status")
	}
	if serviceFile == "" {
		return fmt.Errorf("service: not supported on %s; run `sharefly server` under your own service manager", serviceOS)
	}
	switch args[0] {
	case "install":
		return installService()
	case "uninstall":
		if !serviceInstalled() {
			return errors.New("service: not installed")
		}
		return runServiceAction("uninstall", "")
	case "status":
		if !serviceInstalled() {
			fmt.Println("service: not installed")
			return nil
		}
		return runServiceAction("status", "")
	case "-h", "--help", "help":
		fmt.Print(serviceUsage)
		return nil
	}
	fmt.Fprint(os.Stderr, serviceUsage)
	return fmt.Errorf("service: unknown action %q", args[0])
}

func installService() error {
	if serviceInstalled() {
		return fmt.Errorf("service: already installed (%s); to reinstall: sharefly service uninstall", serviceFile)
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	if u.Uid == "0" {
		return errors.New("service: run install as the user the server should run as, without sudo; it asks for sudo itself")
	}
	cfg, err := parseServerFlags(nil)
	if err != nil {
		return fmt.Errorf("service: the config can't start a server: %w", err)
	}
	c := &client.Client{BaseURL: "http://" + dialAddr(cfg.apiAddr)}
	if _, err := c.List(); !errors.Is(err, client.ErrUnreachable) {
		return errors.New("service: a server is already running; stop it first: sharefly stop")
	}
	spec, err := serviceSpecFor(u, cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "sharefly-service-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(renderService(serviceOS, spec)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := runServiceAction("install", tmp.Name()); err != nil {
		return err
	}
	if err := waitReady(c, startWait, spec.LogFile, 0); err != nil {
		return fmt.Errorf("service installed, but the server isn't answering: %w", err)
	}
	fmt.Printf("sharefly service installed (%s); it starts at boot. Log: %s\n", serviceFile, spec.LogFile)
	return nil
}

func serviceSpecFor(u *user.User, cfg serverConfig) (serviceSpec, error) {
	exe, err := serviceExecutable()
	if err != nil {
		return serviceSpec{}, err
	}
	dirs := []string{filepath.Dir(exe)}
	if cfg.tunnel != tunnelOff {
		bin, err := exec.LookPath("cloudflared")
		if err != nil {
			return serviceSpec{}, fmt.Errorf("tunnel is %q but cloudflared isn't installed: brew install cloudflared", cfg.tunnel)
		}
		dirs = append(dirs, filepath.Dir(bin))
	}
	for _, d := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return serviceSpec{
		User:    u.Username,
		Home:    u.HomeDir,
		Exe:     exe,
		Path:    strings.Join(dirs, ":"),
		LogFile: filepath.Join(cfg.dataDir, logFileName),
	}, nil
}

// serviceExecutable prefers the sharefly on PATH (e.g. Homebrew's link, which survives upgrades) when it is the
// binary running now; otherwise the running binary's own path.
func serviceExecutable() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("sharefly"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil && sameFile(abs, self) {
			return abs, nil
		}
	}
	return self, nil
}

func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
