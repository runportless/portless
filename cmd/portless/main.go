package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/cli"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__daemon" {
		os.Exit(runDaemon(os.Args[2:]))
	}
	application, err := cli.New(os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless:", err)
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}

func runDaemon(args []string) int {
	set := flag.NewFlagSet("__daemon", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	dataDirectory := set.String("data-dir", "", "internal data directory")
	port := set.Int("port", 0, "preferred control port")
	if err := set.Parse(args); err != nil {
		return 2
	}
	paths, err := bootstrap.ResolvePaths(*dataDirectory)
	if err == nil {
		err = bootstrap.RunDaemon(context.Background(), paths, *port)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless daemon:", err)
		return 1
	}
	return 0
}
