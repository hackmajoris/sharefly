package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const (
	tunnelOff   = "off"
	tunnelQuick = "quick"
	tunnelToken = "token"

	quickURLWait     = 30 * time.Second
	tunnelMaxBackoff = 30 * time.Second
	tunnelStopWait   = 5 * time.Second
)

var quickURLPattern = regexp.MustCompile(`https://[a-z0-9]+(?:-[a-z0-9]+)+\.trycloudflare\.com`)

func validateTunnel(v string) error {
	switch v {
	case tunnelOff, tunnelQuick, tunnelToken:
		return nil
	}
	return fmt.Errorf("%q is not a tunnel mode: use off, quick or token", v)
}

// tunnel runs cloudflared as a child of the server, restarting it if it exits, and records the URL of a quick tunnel.
type tunnel struct {
	mode       string
	token      string
	target     string
	bin        string
	minBackoff time.Duration

	mu    sync.Mutex
	url   string
	ready chan struct{}
	once  sync.Once
}

// newTunnel returns nil for mode off. It fails early, before the server binds anything, when the tunnel can't run.
func newTunnel(mode, token, publicAddr string) (*tunnel, error) {
	if err := validateTunnel(mode); err != nil {
		return nil, err
	}
	if mode == tunnelOff {
		return nil, nil
	}
	if mode == tunnelToken && token == "" {
		return nil, errors.New(`tunnel is "token" but tunnel-token is not set: sharefly config set tunnel-token <token>`)
	}
	bin, err := exec.LookPath("cloudflared")
	if err != nil {
		return nil, fmt.Errorf("tunnel is %q but cloudflared isn't installed: brew install cloudflared", mode)
	}
	return &tunnel{
		mode: mode, token: token, target: "http://" + dialAddr(publicAddr), bin: bin,
		minBackoff: time.Second, ready: make(chan struct{}),
	}, nil
}

func (t *tunnel) args() []string {
	if t.mode == tunnelQuick {
		return []string{"tunnel", "--no-autoupdate", "--url", t.target}
	}
	return []string{"tunnel", "--no-autoupdate", "run"}
}

// URL returns the quick tunnel's public URL, or "" until cloudflared has reported one.
func (t *tunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *tunnel) setURL(u string) {
	t.mu.Lock()
	changed := u != t.url
	t.url = u
	t.mu.Unlock()
	if changed {
		log.Printf("public url (quick tunnel): %s", u)
	}
	t.once.Do(func() { close(t.ready) })
}

func (t *tunnel) waitURL(timeout time.Duration) bool {
	select {
	case <-t.ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

// run keeps cloudflared running until ctx is done, backing off between restarts.
func (t *tunnel) run(ctx context.Context) {
	backoff := t.minBackoff
	for {
		started := time.Now()
		err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = t.minBackoff
		}
		log.Printf("cloudflared exited (%v); restarting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, tunnelMaxBackoff)
	}
}

func (t *tunnel) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, t.bin, t.args()...)
	if t.mode == tunnelToken {
		cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+t.token)
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = tunnelStopWait
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			log.Printf("cloudflared: %s", line)
			if t.mode == tunnelQuick {
				if u := quickURLPattern.FindString(line); u != "" {
					t.setURL(u)
				}
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Wait()
	_ = pw.Close()
	<-scanned
	return err
}
