package main

import (
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
	if v := resolve(flagVal, "SHAREFLY_SERVER", c.Server); v != "" {
		return v, nil
	}
	return defaultServer, nil
}

func parseClientArgs(name string, args []string, nargs int, ttl *string) (*client.Client, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	srv := fs.String("server", "", "server API URL (default $SHAREFLY_SERVER, config `server`, or "+defaultServer+")")
	if ttl != nil {
		fs.StringVar(ttl, "ttl", "7d", "time to live: Nd, Nh, Nm or never")
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
	if len(pos) != nargs {
		return nil, nil, fmt.Errorf("%s: expected %d argument(s), got %d", name, nargs, len(pos))
	}
	base, err := resolveServer(*srv)
	if err != nil {
		return nil, nil, err
	}
	return &client.Client{BaseURL: base}, pos, nil
}

func runServe(args []string) error {
	var ttl string
	c, pos, err := parseClientArgs("serve", args, 1, &ttl)
	if err != nil {
		return err
	}
	if err := ensureLocalServer(c, spawnLocalServer, startWait); err != nil {
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
	c, _, err := parseClientArgs("ls", args, 0, nil)
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
	c, pos, err := parseClientArgs("rm", args, 1, nil)
	if err != nil {
		return err
	}
	return c.Delete(pos[0])
}

func runRenew(args []string) error {
	var ttl string
	c, pos, err := parseClientArgs("renew", args, 1, &ttl)
	if err != nil {
		return err
	}
	sh, err := c.Renew(pos[0], ttl)
	if err != nil {
		return err
	}
	return printShares(os.Stdout, []share.Link{sh})
}
