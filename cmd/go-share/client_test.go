package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hackmajoris/go-share/pkg/client"
	"github.com/hackmajoris/go-share/pkg/share"
)

func TestResolveServerPrecedence(t *testing.T) {
	t.Setenv("GO_SHARE_SERVER", "")
	if got := resolveServer(""); got != defaultServer {
		t.Errorf("no flag/env: got %q, want %q", got, defaultServer)
	}
	t.Setenv("GO_SHARE_SERVER", "http://env:1")
	if got := resolveServer(""); got != "http://env:1" {
		t.Errorf("env must be used when flag unset, got %q", got)
	}
	if got := resolveServer("http://flag:1"); got != "http://flag:1" {
		t.Errorf("flag must override env, got %q", got)
	}
}

func TestParseClientArgsFlagsAfterPath(t *testing.T) {
	var ttl string
	c, pos, err := parseClientArgs("serve", []string{"report.html", "--ttl", "1h", "--server", "http://s:1"}, 1, &ttl)
	if err != nil {
		t.Fatal(err)
	}
	if pos[0] != "report.html" || ttl != "1h" || c.BaseURL != "http://s:1" {
		t.Fatalf("documented form `serve <path> --ttl 1h` must work: pos=%v ttl=%q server=%q", pos, ttl, c.BaseURL)
	}
}

func TestParseClientArgsDefaultsAndArity(t *testing.T) {
	var ttl string
	if _, _, err := parseClientArgs("serve", []string{"x.html"}, 1, &ttl); err != nil || ttl != "7d" {
		t.Fatalf("default ttl = %q, %v; want 7d", ttl, err)
	}
	if _, _, err := parseClientArgs("rm", nil, 1, nil); err == nil {
		t.Error("rm without id must error instead of sending a request")
	}
	if _, _, err := parseClientArgs("ls", []string{"stray"}, 0, nil); err == nil {
		t.Error("ls with extra args must error")
	}
}

func TestPrintShares(t *testing.T) {
	exp := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	printShares(&buf, []client.Share{
		{Share: share.Share{ID: "aaaaaaaaaa", Name: "a.html", ExpiresAt: &exp}, URL: "https://s/aaaaaaaaaa/a.html"},
		{Share: share.Share{ID: "bbbbbbbbbb", Name: "site"}, URL: "https://s/bbbbbbbbbb/"},
	})
	out := buf.String()
	for _, want := range []string{"ID", "aaaaaaaaaa", exp.Local().Format(time.DateTime), "https://s/aaaaaaaaaa/a.html", "never"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
