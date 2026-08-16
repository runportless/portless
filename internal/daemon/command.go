package daemon

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/portless-run/portless/internal/installation"
)

// Command parses and runs the private daemon process mode.
func Command(args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__daemon", flag.ContinueOnError)
	set.SetOutput(stderr)
	dataDirectory := set.String("data-dir", "", "internal data directory")
	port := set.Int("port", 0, "preferred control port")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return 2
	}
	layout, err := installation.ResolveLayout(*dataDirectory)
	if err == nil {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = Run(ctx, Config{Layout: layout, PreferredPort: *port})
	}
	if errors.Is(err, ErrExecutableChanged) || errors.Is(err, ErrRestartRequested) {
		executable, executableErr := os.Executable()
		if executableErr == nil {
			executable, executableErr = filepath.EvalSymlinks(executable)
		}
		if executableErr == nil {
			arguments := []string{executable, "__daemon", "--data-dir", layout.Root}
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
		fmt.Fprintln(stderr, "portless daemon:", err)
		return 1
	}
	return 0
}
