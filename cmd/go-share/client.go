package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/hackmajoris/go-share/pkg/client"
)

const defaultServer = "http://macmini:8787"

func resolveServer(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("GO_SHARE_SERVER"); env != "" {
		return env
	}
	return defaultServer
}

func parseClientArgs(name string, args []string, nargs int, ttl *string) (*client.Client, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	srv := fs.String("server", "", "server API URL (default $GO_SHARE_SERVER or "+defaultServer+")")
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
	return &client.Client{BaseURL: resolveServer(*srv)}, pos, nil
}

func runServe(args []string) error {
	var ttl string
	c, pos, err := parseClientArgs("serve", args, 1, &ttl)
	if err != nil {
		return err
	}
	sh, err := c.Upload(pos[0], ttl)
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
	printShares(os.Stdout, shares)
	return nil
}

func printShares(w io.Writer, shares []client.Share) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tEXPIRES\tURL")
	for _, sh := range shares {
		exp := "never"
		if sh.ExpiresAt != nil {
			exp = sh.ExpiresAt.Local().Format(time.DateTime)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sh.ID, sh.Name, exp, sh.URL)
	}
	tw.Flush()
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
	printShares(os.Stdout, []client.Share{sh})
	return nil
}
