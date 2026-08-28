package relay

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	commandInstall   = "install"
	commandRestart   = "restart"
	commandUninstall = "uninstall"
)

func privilegedCommand(action string, args []string, stderr io.Writer) int {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signalContext, 2*time.Minute)
	defer cancel()
	switch action {
	case commandInstall:
		return installCommand(ctx, args, stderr)
	case commandRestart:
		return restartCommand(ctx, args, stderr)
	case commandUninstall:
		return uninstallCommand(ctx, args, stderr)
	default:
		fmt.Fprintln(stderr, "portless relay: unknown privileged command")
		return 2
	}
}

func installCommand(ctx context.Context, args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__install-relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	targetSocket := set.String("socket", "", "private daemon socket")
	dnsTargetSocket := set.String("dns-socket", "", "private daemon DNS socket")
	uid := set.Int("uid", 0, "unprivileged user ID")
	gid := set.Int("gid", 0, "unprivileged group ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err == nil {
		err = installPrivileged(ctx, newHostPlatform(), executable, *targetSocket, *dnsTargetSocket, *uid, *gid)
	}
	if err != nil {
		fmt.Fprintln(stderr, "portless relay install:", err)
		return 1
	}
	return 0
}

func restartCommand(ctx context.Context, args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__restart-relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := restartPrivileged(ctx, newHostPlatform(), *uid); err != nil {
		fmt.Fprintln(stderr, "portless relay restart:", err)
		return 1
	}
	return 0
}

func uninstallCommand(ctx context.Context, args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__uninstall-relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	force := set.Bool("force", false, "allow removing another user's installation")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := uninstallPrivileged(ctx, newHostPlatform(), *uid, *force); err != nil {
		fmt.Fprintln(stderr, "portless relay uninstall:", err)
		return 1
	}
	return 0
}
