// Package environment owns commands that start, stop, inspect, and open a
// Portless environment.
package environment

import "github.com/portless-run/portless/portless-cli/command"

// Commands implements the environment-focused CLI surface.
type Commands struct {
	*command.Context
}

func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
