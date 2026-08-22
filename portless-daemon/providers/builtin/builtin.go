// Package builtin composes the trusted in-process resource plugin registry.
package builtin

import (
	"github.com/runportless/portless/portless-daemon/providers"
	"github.com/runportless/portless/portless-daemon/providers/builtin/mysql"
	"github.com/runportless/portless/portless-daemon/providers/builtin/nats"
	"github.com/runportless/portless/portless-daemon/providers/builtin/postgres"
	"github.com/runportless/portless/portless-daemon/providers/builtin/valkey"
)

// Plugins returns all built-in managed resource plugins.
func Plugins() []providers.Plugin {
	return []providers.Plugin{postgres.New(), valkey.New(), mysql.New(), nats.New()}
}

// Registry returns a validated registry containing all built-in resource plugins.
func Registry() *providers.Registry {
	return providers.MustRegistry(Plugins()...)
}
