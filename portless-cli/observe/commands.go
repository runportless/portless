// Package observe owns read-only service, log, connection, and timeline
// commands.
package observe

import "github.com/runportless/portless/portless-cli/command"

// Commands implements the observability CLI surface.
type Commands struct {
	*command.Context
}

// New returns the observability command collection backed by context.
func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
