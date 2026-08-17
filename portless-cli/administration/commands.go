// Package administration owns daemon, relay, runtime, diagnostics,
// preferences, reset, and uninstall commands.
package administration

import "github.com/portless-run/portless/portless-cli/command"

// Commands implements the machine-level administration CLI surface.
type Commands struct {
	*command.Context
}

func New(context *command.Context) *Commands {
	return &Commands{Context: context}
}
