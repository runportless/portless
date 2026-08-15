package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/cli"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/runtime/supervisor"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__daemon":
			os.Exit(runDaemon(os.Args[2:]))
		case "__ingress":
			os.Exit(runIngress(os.Args[2:]))
		case "__runner":
			os.Exit(runRunner(os.Args[2:]))
		case "__install-ingress":
			os.Exit(runIngressInstaller(os.Args[2:]))
		case "__restart-ingress":
			os.Exit(runIngressRestarter(os.Args[2:]))
		case "__uninstall-ingress":
			os.Exit(runIngressUninstaller(os.Args[2:]))
		}
	}
	application, err := cli.New(os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless:", err)
		os.Exit(1)
	}
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}

func runRunner(args []string) int {
	set := flag.NewFlagSet("__runner", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	manifest := set.String("manifest", "", "private runner manifest")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *manifest == "" {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := supervisor.Run(ctx, *manifest); err != nil {
		fmt.Fprintln(os.Stderr, "portless runner:", err)
		return 1
	}
	return 0
}

func runIngress(args []string) int {
	set := flag.NewFlagSet("__ingress", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	targetSocket := set.String("socket", "", "private daemon socket")
	dnsTargetSocket := set.String("dns-socket", "", "private daemon DNS socket")
	uid := set.Int("uid", 0, "unprivileged user ID")
	gid := set.Int("gid", 0, "unprivileged group ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := ingress.RunRelay(ctx, ingress.RelayConfig{
		TargetSocket: *targetSocket, DNSTargetSocket: *dnsTargetSocket, UID: *uid, GID: *gid, DropPrivileges: true,
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
		err = ingress.InstallPrivileged(context.Background(), executable, *targetSocket, *dnsTargetSocket, *uid, *gid)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless relay install:", err)
		return 1
	}
	return 0
}

func runIngressRestarter(args []string) int {
	set := flag.NewFlagSet("__restart-ingress", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := ingress.RestartPrivileged(context.Background(), *uid); err != nil {
		fmt.Fprintln(os.Stderr, "portless relay restart:", err)
		return 1
	}
	return 0
}

func runIngressUninstaller(args []string) int {
	set := flag.NewFlagSet("__uninstall-ingress", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	uid := set.Int("uid", 0, "requesting user ID")
	force := set.Bool("force", false, "allow removing another user's installation")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	if err := ingress.UninstallPrivileged(context.Background(), *uid, *force); err != nil {
		fmt.Fprintln(os.Stderr, "portless relay uninstall:", err)
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
	if errors.Is(err, bootstrap.ErrExecutableChanged) || errors.Is(err, bootstrap.ErrRestartRequested) {
		executable, executableErr := os.Executable()
		if executableErr == nil {
			executable, executableErr = filepath.EvalSymlinks(executable)
		}
		if executableErr == nil {
			arguments := []string{executable, "__daemon", "--data-dir", paths.Root}
			if *port > 0 {
				arguments = append(arguments, "--port", strconv.Itoa(*port))
			}
			executableErr = syscall.Exec(executable, arguments, os.Environ())
		}
		if executableErr != nil {
			err = fmt.Errorf("replace daemon with updated executable: %w", executableErr)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "portless daemon:", err)
		return 1
	}
	return 0
}
