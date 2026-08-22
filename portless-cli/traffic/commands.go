// Package traffic owns traffic inspection, recording, and fault-injection
// commands.
package traffic

import "github.com/runportless/portless/portless-cli/command"

// Commands implements the traffic CLI surface.
type Commands struct {
	*command.Context
}

// New returns the traffic command collection backed by context.
func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
