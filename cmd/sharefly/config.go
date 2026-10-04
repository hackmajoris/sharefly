package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type config struct {
	PublicURL string `json:"public_url,omitempty"`
	Server    string `json:"server,omitempty"`
}

type configKey struct {
	name, env string
	field     func(*config) *string
}

var configKeys = []configKey{
	{"public-url", "SHAREFLY_PUBLIC_URL", func(c *config) *string { return &c.PublicURL }},
	{"server", "SHAREFLY_SERVER", func(c *config) *string { return &c.Server }},
}

func lookupKey(name string) (configKey, error) {
	for _, k := range configKeys {
		if k.name == name {
			return k, nil
		}
	}
	names := make([]string, len(configKeys))
	for i, k := range configKeys {
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

// resolve returns the first non-empty of flag, env and config file.
func resolve(flagVal, env, fromConfig string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fromConfig
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL like https://share.example.com", raw)
	}
	return nil
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
		v := *k.field(&c)
		switch {
		case os.Getenv(k.env) != "":
			_, _ = fmt.Fprintf(w, "%-11s %s (from $%s, overrides the file)\n", k.name, os.Getenv(k.env), k.env)
		case v != "":
			_, _ = fmt.Fprintf(w, "%-11s %s\n", k.name, v)
		default:
			_, _ = fmt.Fprintf(w, "%-11s (not set)\n", k.name)
		}
	}
	return nil
}

func setConfig(name, value string, restart func() error) error {
	k, err := lookupKey(name)
	if err != nil {
		return err
	}
	if value != "" {
		if err := validateURL(value); err != nil {
			return err
		}
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	*k.field(&c) = value
	if err := saveConfig(c); err != nil {
		return err
	}
	if os.Getenv(k.env) != "" {
		fmt.Fprintf(os.Stderr, "note: $%s is set and overrides the config file\n", k.env)
	}
	if k.name == "public-url" {
		return restart()
	}
	return nil
}

// restartLocalServer applies a config change to a running local server by restarting it with the flags it was
// started with. It is a no-op when no local server is running.
func restartLocalServer() error {
	dir, err := defaultDataDir()
	if err != nil {
		return err
	}
	return restartServer(dir, spawnServer, startWait)
}

func restartServer(dataDir string, spawn func(args []string, dataDir string) (int, string, error), wait time.Duration) error {
	args, err := readServerArgs(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if slices.Contains(args, "--public-url") || slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--public-url=") }) {
		fmt.Fprintln(os.Stderr, "note: the running server was started with --public-url, which overrides the config file")
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
