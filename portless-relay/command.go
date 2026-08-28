// Package relay dispatches the fixed private modes embedded in the Portless
// executable to the relay's runtime and installation owners.
package relay

import (
	"fmt"
	"io"

	"github.com/runportless/portless/portless-relay/installation"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

// Command dispatches one fixed private relay process or lifecycle mode.
func Command(mode string, args []string, stderr io.Writer) int {
	switch mode {
	case "__relay":
		return relayruntime.Command(args, stderr, installation.AuthorizeRuntime)
	case "__install-relay":
		return installation.Command("install", args, stderr)
	case "__restart-relay":
		return installation.Command("restart", args, stderr)
	case "__uninstall-relay":
		return installation.Command("uninstall", args, stderr)
	default:
		fmt.Fprintln(stderr, "portless relay: unknown private command")
		return 2
	}
}
