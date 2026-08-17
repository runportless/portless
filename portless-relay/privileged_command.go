package relay

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// CommandInstall identifies the private privileged installation action.
	CommandInstall = "install"
	// CommandRestart identifies the private privileged restart action.
	CommandRestart = "restart"
	// CommandUninstall identifies the private privileged removal action.
	CommandUninstall = "uninstall"
)

// PrivilegedCommand runs one of the private privileged installation modes.
func PrivilegedCommand(action string, args []string, stderr io.Writer) int {
	switch action {
	case CommandInstall:
		return installCommand(args, stderr)
	case CommandRestart:
		return restartCommand(args, stderr)
	case CommandUninstall:
		return uninstallCommand(args, stderr)
	default:
		fmt.Fprintln(stderr, "portless relay: unknown privileged command")
		return 2
	}
}

func installCommand(args []string, stderr io.Writer) int {
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
		err = InstallPrivileged(context.Background(), executable, *targetSocket, *dnsTargetSocket, *uid, *gid)
	}
	if err != nil {
		fmt.Fprintln(stderr, "portless relay install:", err)
		return 1
	}
	return 0
}

func restartCommand(args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__restart-relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := RestartPrivileged(context.Background(), *uid); err != nil {
		fmt.Fprintln(stderr, "portless relay restart:", err)
		return 1
	}
	return 0
}

func uninstallCommand(args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__uninstall-relay", flag.ContinueOnError)
	set.SetOutput(stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	force := set.Bool("force", false, "allow removing another user's installation")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := UninstallPrivileged(context.Background(), *uid, *force); err != nil {
		fmt.Fprintln(stderr, "portless relay uninstall:", err)
		return 1
	}
	return 0
}
