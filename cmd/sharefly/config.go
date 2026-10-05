package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/hackmajoris/sharefly/pkg/client"
	"github.com/hackmajoris/sharefly/pkg/share"
)

const (
	defaultPublicAddr = "127.0.0.1:7788"
	defaultTTL        = "7d"
)

type config struct {
	PublicURL   string `json:"public_url"`
	Server      string `json:"server"`
	DataDir     string `json:"data_dir"`
	APIAddr     string `json:"api_addr"`
	PublicAddr  string `json:"public_addr"`
	TTL         string `json:"ttl"`
	Tunnel      string `json:"tunnel"`
	TunnelToken string `json:"tunnel_token"`
}

type configKey struct {
	name       string
	help       string
	field      func(*config) *string
	def        func(config) (string, error)
	validate   func(string) error
	serverSide bool
	// derived keys default to another key's value, so the file keeps them empty to keep following it
	derived     bool
	defaultHelp string
	// live keys are picked up by a running server without a restart
	live bool
	// secret keys are hidden by `sharefly config` and have no flag, so they never show up in ps or server.args.json
	secret bool
	// scheme is prepended when a URL value is given without one, e.g. a bare domain
	scheme string
}

var configKeys = []configKey{
	{
		name: "public-url", help: "base URL for share links, e.g. share.example.com (https:// is added)",
		field:    func(c *config) *string { return &c.PublicURL },
		def:      func(c config) (string, error) { return "http://" + or(c.PublicAddr, defaultPublicAddr), nil },
		validate: validateURL, serverSide: true, live: true, derived: true, defaultHelp: "http://<public-addr>", scheme: "https",
	},
	{
		name: "server", help: "server API URL that serve, ls, rm, renew and stop talk to",
		field:    func(c *config) *string { return &c.Server },
		def:      func(c config) (string, error) { return "http://" + dialAddr(or(c.APIAddr, defaultAPIAddr)), nil },
		validate: validateURL, derived: true, defaultHelp: "http://<api-addr>", scheme: "http",
	},
	{
		name: "data-dir", help: "where shares, the pid file and the server log live",
		field:    func(c *config) *string { return &c.DataDir },
		def:      func(config) (string, error) { return defaultDataDir() },
		validate: validateAbsPath, serverSide: true, defaultHelp: "$XDG_STATE_HOME/sharefly or ~/.local/state/sharefly",
	},
	{
		name: "api-addr", help: "management API listen address; use the tailnet IP to accept other devices",
		field:    func(c *config) *string { return &c.APIAddr },
		def:      func(config) (string, error) { return defaultAPIAddr, nil },
		validate: validateAddr, serverSide: true,
	},
	{
		name: "public-addr", help: "file server listen address, what cloudflared points at",
		field:    func(c *config) *string { return &c.PublicAddr },
		def:      func(config) (string, error) { return defaultPublicAddr, nil },
		validate: validateAddr, serverSide: true,
	},
	{
		name: "ttl", help: "default link lifetime: Nm, Nh, Nd or never",
		field:    func(c *config) *string { return &c.TTL },
		def:      func(config) (string, error) { return defaultTTL, nil },
		validate: validateTTL,
	},
	{
		name: "tunnel", help: "tunnel the server runs: off, quick (random trycloudflare.com URL) or token (your Cloudflare tunnel)",
		field:    func(c *config) *string { return &c.Tunnel },
		def:      func(config) (string, error) { return tunnelOff, nil },
		validate: validateTunnel, serverSide: true,
	},
	{
		name: "tunnel-token", help: "Cloudflare tunnel token used by tunnel=token; secret, set only here",
		field:    func(c *config) *string { return &c.TunnelToken },
		def:      func(config) (string, error) { return "", nil },
		validate: validateToken, serverSide: true, secret: true, defaultHelp: "none",
	},
}

var (
	serverKey     = configKeys[1]
	dataDirKey    = configKeys[2]
	apiAddrKey    = configKeys[3]
	publicAddrKey = configKeys[4]
	ttlKey        = configKeys[5]
	tunnelKey     = configKeys[6]
)

// value returns the config file value or the key's default, and whether it came from the file.
func (c config) value(k configKey) (string, bool, error) {
	if v := *k.field(&c); v != "" {
		return v, true, nil
	}
	v, err := k.def(c)
	return v, false, err
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// resolve gives a flag precedence over the config file and the default.
func (c config) resolve(flagVal string, k configKey) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	v, _, err := c.value(k)
	return v, err
}

func lookupKey(name string) (configKey, error) {
	names := make([]string, len(configKeys))
	for i, k := range configKeys {
		if k.name == name {
			return k, nil
		}
		names[i] = k.name
	}
	return configKey{}, fmt.Errorf("unknown key %q (known: %s)", name, strings.Join(names, ", "))
}

func configPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "sharefly", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory for the config file: %w", err)
	}
	return filepath.Join(home, ".config", "sharefly", "config.json"), nil
}

// normalize fixes up a value the way a user most likely meant it: a URL without a scheme gets the key's scheme.
func (k configKey) normalize(v string) string {
	if k.scheme != "" && v != "" && !strings.Contains(v, "://") {
		return k.scheme + "://" + v
	}
	return v
}

// loadConfig reads the config file and rejects invalid values, so every command fails loudly on a bad file.
func loadConfig() (config, error) {
	c, err := readConfig()
	if err != nil {
		return c, err
	}
	for _, k := range configKeys {
		if v := *k.field(&c); v != "" {
			if err := k.validate(v); err != nil {
				path, _ := configPath()
				return c, fmt.Errorf("bad config file %s: %s: %w (fix it with: sharefly config open)", path, k.name, err)
			}
		}
	}
	return c, nil
}

// readConfig parses the config file without validating values, so set and open can repair a broken file.
func readConfig() (config, error) {
	var c config
	path, err := configPath()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("bad config file %s: %w (fix it with: sharefly config open)", path, err)
	}
	for _, k := range configKeys {
		f := k.field(&c)
		*f = k.normalize(*f)
	}
	return c, nil
}

// saveConfig writes every key so the file documents itself: independent keys get their default filled in,
// derived keys stay empty so they keep following the key they derive from.
func saveConfig(c config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	for _, k := range configKeys {
		if f := k.field(&c); *f == "" && !k.derived {
			if *f, err = k.def(c); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL like https://share.example.com", raw)
	}
	return nil
}

func validateAddr(raw string) error {
	if _, port, err := net.SplitHostPort(raw); err != nil || port == "" {
		return fmt.Errorf("%q is not a host:port address like 127.0.0.1:7787", raw)
	}
	return nil
}

func validateAbsPath(raw string) error {
	if !filepath.IsAbs(raw) {
		return fmt.Errorf("%q is not an absolute path", raw)
	}
	return nil
}

func validateToken(raw string) error {
	if len(raw) < 20 || strings.ContainsAny(raw, " \t\n") {
		return errors.New("that doesn't look like a tunnel token: copy the long eyJ... string from the Cloudflare dashboard")
	}
	return nil
}

func validateTTL(raw string) error {
	_, _, err := share.ParseTTL(raw)
	return err
}

func runConfig(args []string) error {
	switch {
	case len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help"):
		configHelp(os.Stdout)
		return flag.ErrHelp
	case len(args) == 0:
		return showConfig(os.Stdout)
	case args[0] == "set" && len(args) == 3:
		return setConfig(args[1], args[2], restartLocalServer)
	case args[0] == "unset" && len(args) == 2:
		return setConfig(args[1], "", restartLocalServer)
	case args[0] == "open" && len(args) == 1:
		return openConfig(editorCommand(), restartLocalServer)
	default:
		configHelp(os.Stderr)
		return errors.New("invalid config command")
	}
}

func configHelp(w io.Writer) {
	path, err := configPath()
	if err != nil {
		path = "~/.config/sharefly/config.json"
	}
	_, _ = fmt.Fprintf(w, `usage: sharefly config [command]

commands:
  (none)              show every setting, its value and whether it is the default
  set <key> <value>   save a setting; a running server applies public-url at once,
                      other server keys restart it
  unset <key>         reset a setting to its default
  open                edit the config file in $VISUAL, $EDITOR or the default app

config file: %s
A flag with the same name (e.g. --api-addr) overrides a setting for one command;
tunnel-token has no flag, so the secret never appears in ps.

keys:
`, path)
	for _, k := range configKeys {
		def := k.defaultHelp
		if def == "" {
			def, _ = k.def(config{})
		}
		scope := "client"
		if k.serverSide {
			scope = "server"
		}
		_, _ = fmt.Fprintf(w, "  %-12s %s\n  %-12s default %s (%s)\n", k.name, k.help, "", def, scope)
	}
}

// editorCommand returns $VISUAL or $EDITOR split into words, or nil when neither is set.
func editorCommand() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if words := strings.Fields(os.Getenv(env)); len(words) > 0 {
			return words
		}
	}
	return nil
}

// openConfig opens the config file in the user's editor. A terminal editor blocks, so afterwards the file is
// validated and a running server restarted if a server-side key changed; the GUI fallback returns immediately.
func openConfig(editor []string, restart func(oldDataDir string) error) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	before, beforeErr := loadConfig()
	if beforeErr == nil {
		if err := saveConfig(before); err != nil {
			return err
		}
	} else if _, statErr := os.Stat(path); statErr != nil {
		return beforeErr
	}
	if editor == nil {
		opener := "xdg-open"
		args := []string{path}
		if runtime.GOOS == "darwin" {
			opener, args = "open", []string{"-t", path}
		}
		if err := exec.Command(opener, args...).Run(); err != nil {
			return fmt.Errorf("open %s: %w (set $EDITOR to use a terminal editor)", path, err)
		}
		fmt.Fprintln(os.Stderr, "after saving, run `sharefly config` to check it. public-url applies at once; for other server keys run `sharefly stop` and `sharefly start`")
		return nil
	}
	cmd := exec.Command(editor[0], append(editor[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}
	after, err := loadConfig()
	if err != nil {
		return err
	}
	if beforeErr != nil {
		// the old file was broken, so there is nothing to compare against; apply the repaired one
		oldDataDir, _, err := after.value(dataDirKey)
		if err != nil {
			return err
		}
		return restart(oldDataDir)
	}
	oldDataDir, _, err := before.value(dataDirKey)
	if err != nil {
		return err
	}
	for _, k := range configKeys {
		if !k.serverSide || k.live {
			continue
		}
		was, _, err := before.value(k)
		if err != nil {
			return err
		}
		now, _, err := after.value(k)
		if err != nil {
			return err
		}
		if was != now {
			return restart(oldDataDir)
		}
	}
	return nil
}

func showConfig(w io.Writer) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "config file: %s\n", path)
	for _, k := range configKeys {
		v, fromFile, err := c.value(k)
		if err != nil {
			return err
		}
		source := "config"
		if def, err := k.def(c); !fromFile || (err == nil && v == def) {
			source = "default"
		}
		if k.secret && v != "" {
			v = v[:4] + "…(hidden)"
		}
		_, _ = fmt.Fprintf(w, "%-12s %-40s (%s)\n", k.name, v, source)
	}
	return nil
}

func setConfig(name, value string, restart func(oldDataDir string) error) error {
	k, err := lookupKey(name)
	if err != nil {
		return err
	}
	value = k.normalize(value)
	if value != "" {
		if err := k.validate(value); err != nil {
			return err
		}
	}
	c, err := readConfig()
	if err != nil {
		return err
	}
	oldDataDir, _, err := c.value(dataDirKey)
	if err != nil {
		return err
	}
	*k.field(&c) = value
	if err := saveConfig(c); err != nil {
		return err
	}
	if k.name == dataDirKey.name {
		if newDir, _, err := c.value(dataDirKey); err == nil && newDir != oldDataDir {
			fmt.Fprintf(os.Stderr, "note: existing shares stay in %s; move them with: mv %q %q\n", oldDataDir, oldDataDir, newDir)
		}
	}
	if !k.serverSide || k.live {
		return nil
	}
	return restart(oldDataDir)
}

// restartLocalServer applies a config change to a running local server by restarting it with the flags it was
// started with. It is a no-op when no local server is running.
func restartLocalServer(dataDir string) error {
	return restartServer(dataDir, spawnServer, startWait)
}

func restartServer(dataDir string, spawn func(args []string, dataDir string) (int, string, error), wait time.Duration) error {
	if serviceInstalled() {
		return restartService(dataDir, wait)
	}
	args, err := readServerArgs(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, k := range configKeys {
		flag := "--" + k.name
		if k.serverSide && slices.ContainsFunc(args, func(a string) bool { return a == flag || strings.HasPrefix(a, flag+"=") }) {
			fmt.Fprintf(os.Stderr, "note: the running server was started with %s, which overrides the config file\n", flag)
		}
	}
	cfg, err := parseServerFlags(args)
	if err != nil {
		return fmt.Errorf("the running server was left as is: %w", err)
	}
	if err := stopServer(filepath.Join(dataDir, pidFileName), shutdownTimeout+time.Second); err != nil {
		if errors.Is(err, errNotRunning) {
			return nil
		}
		return err
	}
	if err := startBackground(cfg, args, spawn, wait); err != nil {
		return fmt.Errorf("restart server: %w", err)
	}
	return nil
}

// restartService applies a config change to the service's server: the service manager restarts it after a
// SIGTERM, so starting one by hand here would only race it for the ports.
func restartService(dataDir string, wait time.Duration) error {
	cfg, err := parseServerFlags(nil)
	if err != nil {
		return fmt.Errorf("the running server was left as is: %w", err)
	}
	if err := stopServer(filepath.Join(dataDir, pidFileName), shutdownTimeout+time.Second); err != nil {
		if errors.Is(err, errNotRunning) {
			return nil
		}
		return err
	}
	if err := waitReady(&client.Client{BaseURL: "http://" + dialAddr(cfg.apiAddr)}, wait, filepath.Join(cfg.dataDir, logFileName), 0); err != nil {
		return fmt.Errorf("restart service: %w", err)
	}
	return nil
}

func readServerArgs(dataDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, argsFileName))
	if err != nil {
		return nil, err
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		return nil, fmt.Errorf("bad %s: %w", argsFileName, err)
	}
	return args, nil
}
