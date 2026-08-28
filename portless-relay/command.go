package relay

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Command dispatches one fixed private relay process or lifecycle mode.
func Command(mode string, args []string, stderr io.Writer) int {
	switch mode {
	case "__relay":
		return relayCommand(args, stderr, newHostPlatform().prepareRuntime)
	case "__install-relay":
		return privilegedCommand(commandInstall, args, stderr)
	case "__restart-relay":
		return privilegedCommand(commandRestart, args, stderr)
	case "__uninstall-relay":
		return privilegedCommand(commandUninstall, args, stderr)
	default:
		fmt.Fprintln(stderr, "portless relay: unknown private command")
		return 2
	}
}

func relayCommand(args []string, stderr io.Writer, prepare func(context.Context) error) int {
	set := flag.NewFlagSet("__relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	targetSocket := set.String("socket", "", "private daemon socket")
	dnsTargetSocket := set.String("dns-socket", "", "private daemon DNS socket")
	uid := set.Int("uid", 0, "unprivileged user ID")
	gid := set.Int("gid", 0, "unprivileged group ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var err error
	if prepare != nil {
		err = prepare(ctx)
	}
	if err == nil {
		err = run(ctx, runtimeConfig{
			TargetSocket: *targetSocket, DNSTargetSocket: *dnsTargetSocket,
			UID: *uid, GID: *gid, DropPrivileges: true,
		})
	}
	if err != nil {
		fmt.Fprintln(stderr, "portless relay:", err)
		return 1
	}
	return 0
}
