package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hackmajoris/sharefly/pkg/client"
	"github.com/hackmajoris/sharefly/pkg/share"
)

const defaultServer = "http://" + defaultAPIAddr

func resolveServer(flagVal string) (string, error) {
	c, err := loadConfig()
	if err != nil {
		return "", err
	}
	return c.resolve(flagVal, serverKey)
}

func parseClientArgs(name string, args []string, nargs int, ttl, tunnel *string, all, password, once *bool) (*client.Client, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	srv := fs.String("server", "", "server API URL (default: config `server`, else http://<api-addr>)")
	if ttl != nil {
		fs.StringVar(ttl, "ttl", "", "time to live: Nd, Nh, Nm or never (default: config `ttl`, else "+defaultTTL+")")
	}
	if tunnel != nil {
		fs.StringVar(tunnel, "tunnel", "", "tunnel for the server serve starts: off, quick or token (default: config `tunnel`)")
	}
	if all != nil {
		fs.BoolVar(all, "all", false, "delete every share instead of one <id>")
	}
	if password != nil {
		fs.BoolVar(password, "password", false, "protect the share with a generated password, printed once on stderr")
	}
	if once != nil {
		fs.BoolVar(once, "once", false, "let one visitor open the share, then remove it a minute later")
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if all != nil && *all {
		nargs = 0
	}
	if len(pos) != nargs {
		return nil, nil, fmt.Errorf("%s: expected %d argument(s), got %d", name, nargs, len(pos))
	}
	if tunnel != nil && *tunnel != "" {
		if err := validateTunnel(*tunnel); err != nil {
			return nil, nil, err
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	base, err := cfg.resolve(*srv, serverKey)
	if err != nil {
		return nil, nil, err
	}
	if ttl != nil {
		if *ttl, err = cfg.resolve(*ttl, ttlKey); err != nil {
			return nil, nil, err
		}
	}
	return &client.Client{BaseURL: base}, pos, nil
}

func runServe(args []string) error {
	var ttl, tunnel string
	var password, once bool
	c, pos, err := parseClientArgs("serve", args, 1, &ttl, &tunnel, nil, &password, &once)
	if err != nil {
		return err
	}
	if err := ensureLocalServer(c, tunnel, spawnLocalServer, startWait); err != nil {
		return err
	}
	sh, skipped, err := c.Upload(pos[0], ttl, password, once)
	for _, rel := range skipped {
		fmt.Fprintf(os.Stderr, "warning: skipping %s (not a regular file)\n", rel)
	}
	if err != nil {
		return err
	}
	if sh.Password != "" {
		fmt.Fprintf(os.Stderr, "password: %s (shown only now)\n", sh.Password)
	}
	fmt.Println(sh.URL)
	return nil
}

// openBrowser opens a URL in the default browser; tests replace it.
var openBrowser = func(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Run()
}

// runDashboard opens the server's management page, starting a local server first when needed, like serve.
func runDashboard(args []string) error {
	c, _, err := parseClientArgs("dashboard", args, 0, nil, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	if err := ensureLocalServer(c, "", spawnLocalServer, startWait); err != nil {
		return err
	}
	if _, err := c.List(); err != nil {
		return err
	}
	url := strings.TrimSuffix(c.BaseURL, "/") + "/"
	fmt.Println(url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser (%v); open the URL above yourself\n", err)
	}
	return nil
}

func runList(args []string) error {
	c, _, err := parseClientArgs("ls", args, 0, nil, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	shares, err := c.List()
	if err != nil {
		return err
	}
	return printShares(os.Stdout, shares)
}

func printShares(w io.Writer, shares []share.Link) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tEXPIRES\tURL")
	for _, sh := range shares {
		exp := "never"
		if sh.ExpiresAt != nil {
			exp = sh.ExpiresAt.Local().Format(time.DateTime)
		}
		if sh.Once {
			exp += " (once)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sh.ID, sh.Name, exp, sh.URL)
	}
	return tw.Flush()
}

func runRm(args []string) error {
	var all bool
	c, pos, err := parseClientArgs("rm", args, 1, nil, nil, &all, nil, nil)
	if err != nil {
		return err
	}
	if !all {
		return c.Delete(pos[0])
	}
	shares, err := c.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, sh := range shares {
		if err := c.Delete(sh.ID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sh.ID, err))
		}
	}
	return errors.Join(errs...)
}

func runRenew(args []string) error {
	var ttl string
	c, pos, err := parseClientArgs("renew", args, 1, &ttl, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	sh, err := c.Renew(pos[0], ttl)
	if err != nil {
		return err
	}
	return printShares(os.Stdout, []share.Link{sh})
}
