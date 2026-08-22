// Package environment owns commands that start, stop, inspect, and open a
// Portless environment.
package environment

import "github.com/runportless/portless/portless-cli/command"

// Commands implements the environment-focused CLI surface.
type Commands struct {
	*command.Context
}

// New returns the environment command collection backed by context.
func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
