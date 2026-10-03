package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hackmajoris/go-share/pkg/server"
	"github.com/hackmajoris/go-share/pkg/share"
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
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.StringVar(&cfg.apiAddr, "api-addr", "", "management API listen address (tailnet IP:port, required)")
	fs.StringVar(&cfg.publicAddr, "public-addr", "127.0.0.1:8080", "public file server listen address")
	fs.StringVar(&cfg.dataDir, "data-dir", "", "data directory (default ~/go-share)")
	fs.StringVar(&cfg.publicURL, "public-url", "", "public base URL, e.g. https://share.example.com (required)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.publicURL == "" {
		return cfg, errors.New("--public-url is required")
	}
	if cfg.apiAddr == "" {
		return cfg, errors.New("--api-addr is required")
	}
	if cfg.dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("no --data-dir and no home directory: %w", err)
		}
		cfg.dataDir = filepath.Join(home, "go-share")
	}
	return cfg, nil
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
		apiLn.Close()
		return err
	}

	if err := share.Reconcile(store, sharesDir, tmpDir); err != nil {
		log.Printf("reconcile: %v", err)
	}
	if err := share.Sweep(store, sharesDir, time.Now()); err != nil {
		log.Printf("sweep: %v", err)
	}

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
