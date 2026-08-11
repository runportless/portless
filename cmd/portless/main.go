package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/cli"
	"github.com/portless-run/portless/internal/ingress"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__daemon":
			os.Exit(runDaemon(os.Args[2:]))
		case "__ingress":
			os.Exit(runIngress(os.Args[2:]))
		case "__install-ingress":
			os.Exit(runIngressInstaller(os.Args[2:]))
		}
	}
	application, err := cli.New(os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless:", err)
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}

func runIngress(args []string) int {
	set := flag.NewFlagSet("__ingress", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	targetSocket := set.String("socket", "", "private daemon socket")
	uid := set.Int("uid", 0, "unprivileged user ID")
	gid := set.Int("gid", 0, "unprivileged group ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := ingress.RunRelay(ctx, ingress.RelayConfig{
		TargetSocket: *targetSocket, UID: *uid, GID: *gid, DropPrivileges: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless ingress:", err)
		return 1
	}
	return 0
}

func runIngressInstaller(args []string) int {
	set := flag.NewFlagSet("__install-ingress", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	targetSocket := set.String("socket", "", "private daemon socket")
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
		err = ingress.InstallPrivileged(context.Background(), executable, *targetSocket, *uid, *gid)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless setup:", err)
		return 1
	}
	return 0
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
