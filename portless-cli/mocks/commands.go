// Package mocks owns CLI commands for environment-scoped HTTP mock providers.
package mocks

import "github.com/runportless/portless/portless-cli/command"

// Commands implements the mock-provider CLI surface.
type Commands struct {
	*command.Context
}

// New returns the mock command collection backed by context.
func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
