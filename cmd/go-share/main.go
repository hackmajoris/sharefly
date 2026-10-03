package main

import (
	"fmt"
	"os"
)

const usage = `usage: go-share <command> [flags]

commands:
  serve     share a file or folder: serve <path> [--ttl 7d] [--server URL]
  ls        list shares
  rm        delete a share: rm <id>
  renew     extend a share: renew <id> [--ttl 7d]
  server    run the share server
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = runServer(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "ls":
		err = runList(os.Args[2:])
	case "rm":
		err = runRm(os.Args[2:])
	case "renew":
		err = runRenew(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-share:", err)
		os.Exit(1)
	}
}
