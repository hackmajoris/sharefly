package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

const (
	defaultPublicAddr = "127.0.0.1:8080"
	defaultTTL        = "7d"
)

type config struct {
	PublicURL  string `json:"public_url,omitempty"`
	Server     string `json:"server,omitempty"`
	DataDir    string `json:"data_dir,omitempty"`
	APIAddr    string `json:"api_addr,omitempty"`
	PublicAddr string `json:"public_addr,omitempty"`
	TTL        string `json:"ttl,omitempty"`
}

type configKey struct {
	name       string
	field      func(*config) *string
	def        func(config) (string, error)
	validate   func(string) error
	serverSide bool
}

var configKeys = []configKey{
	{"public-url", func(c *config) *string { return &c.PublicURL },
		func(c config) (string, error) { return "http://" + or(c.PublicAddr, defaultPublicAddr), nil }, validateURL, true},
	{"server", func(c *config) *string { return &c.Server },
		func(c config) (string, error) { return "http://" + dialAddr(or(c.APIAddr, defaultAPIAddr)), nil }, validateURL, false},
	{"data-dir", func(c *config) *string { return &c.DataDir },
		func(config) (string, error) { return defaultDataDir() }, validateAbsPath, true},
	{"api-addr", func(c *config) *string { return &c.APIAddr },
		func(config) (string, error) { return defaultAPIAddr, nil }, validateAddr, true},
	{"public-addr", func(c *config) *string { return &c.PublicAddr },
		func(config) (string, error) { return defaultPublicAddr, nil }, validateAddr, true},
	{"ttl", func(c *config) *string { return &c.TTL },
		func(config) (string, error) { return defaultTTL, nil }, validateTTL, false},
}

var (
	serverKey     = configKeys[1]
	dataDirKey    = configKeys[2]
	apiAddrKey    = configKeys[3]
	publicAddrKey = configKeys[4]
	ttlKey        = configKeys[5]
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

func loadConfig() (config, error) {
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
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("bad config file %s: %w", path, err)
	}
	return c, nil
}

func saveConfig(c config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
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
		return fmt.Errorf("%q is not a host:port address like 127.0.0.1:8787", raw)
	}
	return nil
}

func validateAbsPath(raw string) error {
	if !filepath.IsAbs(raw) {
		return fmt.Errorf("%q is not an absolute path", raw)
	}
	return nil
}

func validateTTL(raw string) error {
	_, _, err := share.ParseTTL(raw)
	return err
}

func runConfig(args []string) error {
	switch {
	case len(args) == 0:
		return showConfig(os.Stdout)
	case args[0] == "set" && len(args) == 3:
		return setConfig(args[1], args[2], restartLocalServer)
	case args[0] == "unset" && len(args) == 2:
		return setConfig(args[1], "", restartLocalServer)
	default:
		return errors.New("usage: sharefly config [set <key> <value> | unset <key>]")
	}
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
		source := "default"
		if fromFile {
			source = "config"
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
	if value != "" {
		if err := k.validate(value); err != nil {
			return err
		}
	}
	c, err := loadConfig()
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
	if !k.serverSide {
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
	if err := stopServer(filepath.Join(dataDir, pidFileName), shutdownTimeout+time.Second); err != nil {
		if errors.Is(err, errNotRunning) {
			return nil
		}
		return err
	}
	cfg, err := parseServerFlags(args)
	if err != nil {
		return err
	}
	if err := startBackground(cfg, args, spawn, wait); err != nil {
		return fmt.Errorf("restart server: %w", err)
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
