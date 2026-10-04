package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hackmajoris/sharefly/pkg/server"
	"github.com/hackmajoris/sharefly/pkg/share"
)

const (
	maxUploadBytes    = 100 << 20
	sweepInterval     = 5 * time.Minute
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

type serverConfig struct {
	apiAddr    string
	publicAddr string
	dataDir    string
	publicURL  string
	// publicURLFlag is true when --public-url (or an explicit --tunnel off, which means local links) pinned the URL;
	// otherwise it follows the config file live
	publicURLFlag bool
	tunnel        string
	tunnelToken   string
}

func parseServerFlags(args []string) (serverConfig, error) {
	var cfg serverConfig
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.StringVar(&cfg.apiAddr, "api-addr", "", "management API listen address; the tailnet IP accepts other devices (default: config `api-addr`, else "+defaultAPIAddr+")")
	fs.StringVar(&cfg.publicAddr, "public-addr", "", "file server listen address (default: config `public-addr`, else "+defaultPublicAddr+")")
	fs.StringVar(&cfg.dataDir, "data-dir", "", "data directory (default: config `data-dir`, else $XDG_STATE_HOME/sharefly or ~/.local/state/sharefly)")
	fs.StringVar(&cfg.publicURL, "public-url", "", "public base URL for links (default: config `public-url`, else http://<public-addr>)")
	fs.StringVar(&cfg.tunnel, "tunnel", "", "tunnel to run: off, quick or token (default: config `tunnel`, else off)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	tunnelFlag := cfg.tunnel
	file, err := loadConfig()
	if err != nil {
		return cfg, err
	}
	if cfg.apiAddr, err = file.resolve(cfg.apiAddr, apiAddrKey); err != nil {
		return cfg, err
	}
	if cfg.publicAddr, err = file.resolve(cfg.publicAddr, publicAddrKey); err != nil {
		return cfg, err
	}
	if cfg.dataDir, err = file.resolve(cfg.dataDir, dataDirKey); err != nil {
		return cfg, err
	}
	if cfg.tunnel, err = file.resolve(cfg.tunnel, tunnelKey); err != nil {
		return cfg, err
	}
	if err := validateTunnel(cfg.tunnel); err != nil {
		return cfg, err
	}
	cfg.tunnelToken = file.TunnelToken
	cfg.publicURLFlag = cfg.publicURL != ""
	switch {
	case cfg.tunnel == tunnelToken && cfg.tunnelToken == "":
		return cfg, errNoTunnelToken
	case cfg.tunnel == tunnelToken && !cfg.publicURLFlag && file.PublicURL == "":
		return cfg, errNoPublicURL
	case tunnelFlag == tunnelOff && !cfg.publicURLFlag:
		cfg.publicURL, cfg.publicURLFlag = "http://"+cfg.publicAddr, true
	}
	if cfg.publicURL == "" {
		cfg.publicURL = or(file.PublicURL, "http://"+cfg.publicAddr)
	}
	return cfg, nil
}

// defaultDataDir follows the XDG state dir: shares expire, so they are state, not lasting user data.
func defaultDataDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "sharefly"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no --data-dir and no home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "sharefly"), nil
}

func runServer(args []string) error {
	cfg, err := parseServerFlags(args)
	if err != nil {
		return err
	}
	store, err := share.Open(filepath.Join(cfg.dataDir, "shares.json"))
	if err != nil {
		return err
	}
	tun, err := newTunnel(cfg.tunnel, cfg.tunnelToken, cfg.publicAddr)
	if err != nil {
		return err
	}
	api := &server.API{Store: store, DataDir: cfg.dataDir, MaxBytes: maxUploadBytes, PublicURLFunc: publicURLFunc(cfg, tun)}
	sharesDir, tmpDir := api.SharesDir(), api.TmpDir()
	for _, d := range []string{sharesDir, tmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	apiLn, err := net.Listen("tcp", cfg.apiAddr)
	if err != nil {
		return err
	}
	publicLn, err := net.Listen("tcp", cfg.publicAddr)
	if err != nil {
		_ = apiLn.Close()
		return err
	}

	if err := share.Reconcile(store, sharesDir, tmpDir); err != nil {
		_ = apiLn.Close()
		_ = publicLn.Close()
		return fmt.Errorf("reconcile: %w", err)
	}
	if err := share.Sweep(store, sharesDir, time.Now()); err != nil {
		log.Printf("sweep: %v", err)
	}
	pidFile := filepath.Join(cfg.dataDir, pidFileName)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		_ = apiLn.Close()
		_ = publicLn.Close()
		return err
	}
	defer func() { _ = os.Remove(pidFile) }()
	argsFile := filepath.Join(cfg.dataDir, argsFileName)
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if err := os.WriteFile(argsFile, argsJSON, 0o644); err != nil {
		return err
	}
	defer func() { _ = os.Remove(argsFile) }()

	servers := []*http.Server{
		{Handler: api.Handler(), ReadHeaderTimeout: readHeaderTimeout},
		{Handler: server.FilesHandler(sharesDir, store), ReadHeaderTimeout: readHeaderTimeout},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if tun != nil {
		tunDone := make(chan struct{})
		go func() { tun.run(ctx); close(tunDone) }()
		defer func() { stop(); <-tunDone }()
		log.Printf("tunnel: %s via %s", cfg.tunnel, tun.bin)
		if cfg.tunnel == tunnelQuick && !tun.waitURL(quickURLWait) {
			log.Printf("tunnel: no quick tunnel URL after %s; links use %s until it arrives", quickURLWait, api.Base())
		}
	}

	errc := make(chan error, len(servers))
	for i, ln := range []net.Listener{apiLn, publicLn} {
		go func() { errc <- servers[i].Serve(ln) }()
	}
	log.Printf("api on %s, files on %s, data in %s", cfg.apiAddr, cfg.publicAddr, cfg.dataDir)

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	var serveErr error
loop:
	for {
		select {
		case <-ticker.C:
			if err := share.Sweep(store, sharesDir, time.Now()); err != nil {
				log.Printf("sweep: %v", err)
			}
		case serveErr = <-errc:
			break loop
		case <-ctx.Done():
			break loop
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
	return serveErr
}

// livePublicURL re-reads public-url from the config file whenever the file changes, so editing the file (by any
// means) changes new links without restarting the server. A broken file keeps the last good value.
type livePublicURL struct {
	mu         sync.Mutex
	modTime    time.Time
	url        string
	publicAddr string
}

func newLivePublicURL(initial, publicAddr string) *livePublicURL {
	l := &livePublicURL{url: initial, publicAddr: publicAddr}
	l.modTime = configModTime()
	return l
}

func configModTime() time.Time {
	path, err := configPath()
	if err != nil {
		return time.Time{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func (l *livePublicURL) get() string {
	mod := configModTime()
	l.mu.Lock()
	defer l.mu.Unlock()
	if mod.Equal(l.modTime) {
		return l.url
	}
	l.modTime = mod
	c, err := loadConfig()
	if err != nil {
		log.Printf("config: %v; links keep using %s", err, l.url)
		return l.url
	}
	if u := or(c.PublicURL, "http://"+l.publicAddr); u != l.url {
		log.Printf("public url changed to %s", u)
		l.url = u
	}
	return l.url
}

// publicURLFunc picks the base URL for links: an explicit --public-url, else a quick tunnel's URL once known,
// else public-url from the config file, re-read whenever the file changes.
func publicURLFunc(cfg serverConfig, tun *tunnel) func() string {
	if cfg.publicURLFlag {
		return func() string { return cfg.publicURL }
	}
	live := newLivePublicURL(cfg.publicURL, cfg.publicAddr)
	return func() string {
		if tun != nil && tun.mode == tunnelQuick {
			if u := tun.URL(); u != "" {
				return u
			}
		}
		return live.get()
	}
}
