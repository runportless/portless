package supervisor

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Command parses and runs the private service-supervisor process mode.
func Command(args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("__runner", flag.ContinueOnError)
	set.SetOutput(stderr)
	manifest := set.String("manifest", "", "private runner manifest")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *manifest == "" {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := Run(ctx, *manifest); err != nil {
		fmt.Fprintln(stderr, "portless runner:", err)
		return 1
	}
	return 0
}
