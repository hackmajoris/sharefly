package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usage = `usage: sharefly <command> [flags]

commands:
  serve     share a file or folder: serve <path> [--ttl 7d] [--server URL]
  ls        list shares
  rm        delete a share: rm <id>
  renew     extend a share: renew <id> [--ttl 7d]
  start     start the share server in the background (serve does this automatically)
  server    run the share server in the foreground
  stop      stop the local share server
  config    show or change settings: config [set <key> <value> | unset <key> | open]
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "start":
		err = runStart(args[1:])
	case "server":
		err = runServer(args[1:])
	case "stop":
		err = runStop(args[1:])
	case "config":
		err = runConfig(args[1:])
	case "serve":
		err = runServe(args[1:])
	case "ls":
		err = runList(args[1:])
	case "rm":
		err = runRm(args[1:])
	case "renew":
		err = runRenew(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sharefly:", err)
		return 1
	}
	return 0
}
