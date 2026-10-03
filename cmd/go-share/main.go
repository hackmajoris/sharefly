package main

import (
	"fmt"
	"os"
)

const usage = `usage: go-share <command> [flags]

commands:
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
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-share:", err)
		os.Exit(1)
	}
}
