package main

import (
	"context"
	"fmt"
	"os"

	"github.com/portless-run/portless/internal/cli"
	"github.com/portless-run/portless/internal/daemon"
	"github.com/portless-run/portless/internal/relay"
	relayinstall "github.com/portless-run/portless/internal/relay/install"
	"github.com/portless-run/portless/internal/runtime/supervisor"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__daemon":
			os.Exit(daemon.Command(os.Args[2:], os.Stderr))
		case "__relay":
			os.Exit(relay.Command(os.Args[2:], os.Stderr, relayinstall.PrepareRuntime))
		case "__runner":
			os.Exit(supervisor.Command(os.Args[2:], os.Stderr))
		case "__install-relay":
			os.Exit(relayinstall.Command(relayinstall.CommandInstall, os.Args[2:], os.Stderr))
		case "__restart-relay":
			os.Exit(relayinstall.Command(relayinstall.CommandRestart, os.Args[2:], os.Stderr))
		case "__uninstall-relay":
			os.Exit(relayinstall.Command(relayinstall.CommandUninstall, os.Args[2:], os.Stderr))
		}
	}
	application, err := cli.New(os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless:", err)
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}
