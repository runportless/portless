// Package traffic owns traffic inspection, recording, and fault-injection
// commands.
package traffic

import "github.com/portless-run/portless/portless-cli/command"

// Commands implements the traffic CLI surface.
type Commands struct {
	*command.Context
}

func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
