// Package projects owns project, source, environment, and binding
// configuration commands.
package projects

import "github.com/portless-run/portless/portless-cli/command"

// Commands implements the project configuration CLI surface.
type Commands struct {
	*command.Context
}

// New returns the project command collection backed by context.
func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
