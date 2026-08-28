package runtime

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Command parses the fixed private relay runtime arguments, authorizes their
// identity, and serves the privileged listeners until interrupted.
func Command(args []string, stderr io.Writer, authorize func(context.Context, Identity) error) int {
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
	identity := Identity{TargetSocket: *targetSocket, DNSTargetSocket: *dnsTargetSocket, UID: *uid, GID: *gid}
	err := ValidateIdentity(identity)
	if err == nil && authorize != nil {
		err = authorize(ctx, identity)
	}
	if err == nil {
		err = run(ctx, config{identity: identity, DropPrivileges: true})
	}
	if err != nil {
		fmt.Fprintln(stderr, "portless relay:", err)
		return 1
	}
	return 0
}
