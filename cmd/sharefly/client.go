package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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

func parseClientArgs(name string, args []string, nargs int, ttl, tunnel *string, all *bool) (*client.Client, []string, error) {
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
	c, pos, err := parseClientArgs("serve", args, 1, &ttl, &tunnel, nil)
	if err != nil {
		return err
	}
	if err := ensureLocalServer(c, tunnel, spawnLocalServer, startWait); err != nil {
		return err
	}
	sh, skipped, err := c.Upload(pos[0], ttl)
	for _, rel := range skipped {
		fmt.Fprintf(os.Stderr, "warning: skipping %s (not a regular file)\n", rel)
	}
	if err != nil {
		return err
	}
	fmt.Println(sh.URL)
	return nil
}

func runList(args []string) error {
	c, _, err := parseClientArgs("ls", args, 0, nil, nil, nil)
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
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sh.ID, sh.Name, exp, sh.URL)
	}
	return tw.Flush()
}

func runRm(args []string) error {
	var all bool
	c, pos, err := parseClientArgs("rm", args, 1, nil, nil, &all)
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
	c, pos, err := parseClientArgs("renew", args, 1, &ttl, nil, nil)
	if err != nil {
		return err
	}
	sh, err := c.Renew(pos[0], ttl)
	if err != nil {
		return err
	}
	return printShares(os.Stdout, []share.Link{sh})
}
