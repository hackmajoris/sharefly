package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

// Precedence flag > config file > default; the default follows api-addr so a configured host talks to itself.
func TestResolveServerPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	check := func(flagVal, want string) {
		t.Helper()
		got, err := resolveServer(flagVal)
		if err != nil || got != want {
			t.Errorf("resolveServer(%q) = %q, %v; want %q", flagVal, got, err, want)
		}
	}
	check("", defaultServer)
	if err := saveConfig(config{APIAddr: "100.64.0.1:8787"}); err != nil {
		t.Fatal(err)
	}
	check("", "http://100.64.0.1:8787")
	if err := saveConfig(config{APIAddr: "0.0.0.0:8787"}); err != nil {
		t.Fatal(err)
	}
	check("", "http://127.0.0.1:8787")
	if err := saveConfig(config{Server: "http://file:1", APIAddr: "100.64.0.1:8787"}); err != nil {
		t.Fatal(err)
	}
	check("", "http://file:1")
	check("http://flag:1", "http://flag:1")
}

func TestParseClientArgsTTLFromConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var ttl string
	if _, _, err := parseClientArgs("serve", []string{"x"}, 1, &ttl, nil); err != nil || ttl != defaultTTL {
		t.Fatalf("ttl = %q, %v; want %s", ttl, err, defaultTTL)
	}
	if err := saveConfig(config{TTL: "1h"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseClientArgs("serve", []string{"x"}, 1, &ttl, nil); err != nil || ttl != "1h" {
		t.Fatalf("ttl = %q, %v; want the configured 1h", ttl, err)
	}
	if _, _, err := parseClientArgs("serve", []string{"x", "--ttl", "never"}, 1, &ttl, nil); err != nil || ttl != "never" {
		t.Fatalf("ttl = %q, %v; flag must override config", ttl, err)
	}
}

func TestParseClientArgsFlagsAfterPath(t *testing.T) {
	var ttl string
	c, pos, err := parseClientArgs("serve", []string{"report.html", "--ttl", "1h", "--server", "http://s:1"}, 1, &ttl, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pos[0] != "report.html" || ttl != "1h" || c.BaseURL != "http://s:1" {
		t.Fatalf("documented form `serve <path> --ttl 1h` must work: pos=%v ttl=%q server=%q", pos, ttl, c.BaseURL)
	}
}

func TestParseClientArgsDefaultsAndArity(t *testing.T) {
	var ttl string
	if _, _, err := parseClientArgs("serve", []string{"x.html"}, 1, &ttl, nil); err != nil || ttl != "7d" {
		t.Fatalf("default ttl = %q, %v; want 7d", ttl, err)
	}
	if _, _, err := parseClientArgs("rm", nil, 1, nil, nil); err == nil {
		t.Error("rm without id must error instead of sending a request")
	}
	if _, _, err := parseClientArgs("ls", []string{"stray"}, 0, nil, nil); err == nil {
		t.Error("ls with extra args must error")
	}
}

func TestPrintShares(t *testing.T) {
	exp := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := printShares(&buf, []share.Link{
		{Share: share.Share{ID: "aaaaaaaaaa", Name: "a.html", ExpiresAt: &exp}, URL: "https://s/aaaaaaaaaa/a.html"},
		{Share: share.Share{ID: "bbbbbbbbbb", Name: "site"}, URL: "https://s/bbbbbbbbbb/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want header + 2:\n%s", len(lines), buf.String())
	}
	rows := [][]string{
		{"ID", "NAME", "EXPIRES", "URL"},
		{"aaaaaaaaaa", "a.html", exp.Local().Format(time.DateTime), "https://s/aaaaaaaaaa/a.html"},
		{"bbbbbbbbbb", "site", "never", "https://s/bbbbbbbbbb/"},
	}
	for i, want := range rows {
		for _, field := range want {
			if !strings.Contains(lines[i], field) {
				t.Errorf("line %d %q missing %q", i, lines[i], field)
			}
		}
	}
}

// The user's exact form `serve <path> --tunnel quick` must parse, and a typo must fail before any connection.
func TestParseClientArgsTunnel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var ttl, tunnel string
	if _, pos, err := parseClientArgs("serve", []string{"tmp", "--tunnel", "quick"}, 1, &ttl, &tunnel); err != nil || tunnel != tunnelQuick || pos[0] != "tmp" {
		t.Fatalf("pos=%v tunnel=%q err=%v", pos, tunnel, err)
	}
	if _, _, err := parseClientArgs("serve", []string{"tmp", "--tunnel", "quik"}, 1, &ttl, &tunnel); err == nil {
		t.Fatal("unknown tunnel mode must be rejected")
	}
}
