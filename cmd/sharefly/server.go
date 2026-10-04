package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
}

func parseServerFlags(args []string) (serverConfig, error) {
	var cfg serverConfig
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.StringVar(&cfg.apiAddr, "api-addr", defaultAPIAddr, "management API listen address (use the tailnet IP to accept other devices)")
	fs.StringVar(&cfg.publicAddr, "public-addr", "127.0.0.1:8080", "public file server listen address")
	fs.StringVar(&cfg.dataDir, "data-dir", "", "data directory (default ~/sharefly)")
	fs.StringVar(&cfg.publicURL, "public-url", "", "public base URL for links (default $SHAREFLY_PUBLIC_URL or http://<public-addr>)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.publicURL == "" {
		cfg.publicURL = os.Getenv("SHAREFLY_PUBLIC_URL")
	}
	if cfg.publicURL == "" {
		cfg.publicURL = "http://" + cfg.publicAddr
	}
	if cfg.dataDir == "" {
		dir, err := defaultDataDir()
		if err != nil {
			return cfg, err
		}
		cfg.dataDir = dir
	}
	return cfg, nil
}

func defaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no --data-dir and no home directory: %w", err)
	}
	return filepath.Join(home, "sharefly"), nil
}

func runStart(args []string) error {
	cfg, err := parseServerFlags(args)
	if err != nil {
		return err
	}
	store, err := share.Open(filepath.Join(cfg.dataDir, "shares.json"))
	if err != nil {
		return err
	}
	api := &server.API{Store: store, DataDir: cfg.dataDir, PublicURL: cfg.publicURL, MaxBytes: maxUploadBytes}
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

	servers := []*http.Server{
		{Handler: api.Handler(), ReadHeaderTimeout: readHeaderTimeout},
		{Handler: server.FilesHandler(sharesDir), ReadHeaderTimeout: readHeaderTimeout},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
