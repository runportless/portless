// Package observe owns read-only service, log, connection, and timeline
// commands.
package observe

import "github.com/portless-run/portless/portless-cli/command"

// Commands implements the observability CLI surface.
type Commands struct {
	*command.Context
}

func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
