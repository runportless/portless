package main

import (
	"context"
	"fmt"
	"os"

	"github.com/runportless/portless/portless-cli"
	"github.com/runportless/portless/portless-daemon"
	"github.com/runportless/portless/portless-relay"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__daemon":
			os.Exit(daemon.Command(os.Args[2:], os.Stderr, daemon.BuildInfo{Version: cli.Version, Distribution: cli.Distribution, Commit: cli.Commit}))
		case "__relay":
			os.Exit(relay.Command(os.Args[2:], os.Stderr, relay.PrepareRuntime))
		case "__runner":
			os.Exit(daemon.RunnerCommand(os.Args[2:], os.Stderr))
		case "__install-relay":
			os.Exit(relay.PrivilegedCommand(relay.CommandInstall, os.Args[2:], os.Stderr))
		case "__restart-relay":
			os.Exit(relay.PrivilegedCommand(relay.CommandRestart, os.Args[2:], os.Stderr))
		case "__uninstall-relay":
			os.Exit(relay.PrivilegedCommand(relay.CommandUninstall, os.Args[2:], os.Stderr))
		}
	}
	application, err := cli.New(os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless:", err)
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}
